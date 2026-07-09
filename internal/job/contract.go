package job

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

const (
	ContractSchemaV1      = "agent-team.contract.v1"
	ContractDefaultGates  = "smoke"
	ContractDefaultVerify = "review"
)

var (
	contractClauseIDPattern      = regexp.MustCompile(`^[A-Z][A-Z0-9_-]*[0-9]+$`)
	freeformCriterionIDPattern   = regexp.MustCompile(`^AC[0-9]+$`)
	explicitCriterionLinePattern = regexp.MustCompile(`^\s*(?:[-*]\s*)?(?:\[[ xX]\]\s*)?([A-Za-z][A-Za-z0-9_-]*[0-9]+)[\).:\-]\s+(.+)$`)
	numberedCriterionLinePattern = regexp.MustCompile(`^\s*(?:[-*]\s*)?(?:\[[ xX]\]\s*)?(?:[0-9]+[\).])\s+(.+)$`)
	requiredTrailerPattern       = regexp.MustCompile("(?i)required(?:\\s+pr)?\\s+trailer\\s*:\\s*(?:`([^`]+)`|([^\\n]+))")
	verifyHintPattern            = regexp.MustCompile(`(?i)\s+\(verify:\s*([^)]+)\)\s*$`)
	githubIssueRefPattern        = regexp.MustCompile(`#([0-9]+)`)
)

// Contract is the v1 durable handoff envelope stored under [contract] in a job.
type Contract struct {
	Schema      string              `toml:"schema"`
	WorkItem    string              `toml:"work_item"`
	Deliverable string              `toml:"deliverable"`
	Trailer     string              `toml:"trailer,omitempty"`
	Gates       string              `toml:"gates"`
	Scope       []string            `toml:"scope,omitempty"`
	Criteria    []ContractCriterion `toml:"criteria,omitempty"`
}

// ContractCriterion is one stable, citable acceptance clause.
type ContractCriterion struct {
	ID     string `toml:"id"`
	Text   string `toml:"text"`
	Verify string `toml:"verify,omitempty"`
}

// ContractCompileOptions carries the textual context used to compile a v1
// contract from an already-normalized durable job.
type ContractCompileOptions struct {
	Text            string
	RequiredTrailer string
	ExplicitEpic    string
	Gates           string
	Scope           []string
}

type criterionSection struct {
	text          string
	allowNumbered bool
}

// ValidateContract checks the persisted v1 contract shape. A nil contract is
// valid so older durable job records keep round-tripping unchanged.
func ValidateContract(c *Contract) error {
	if c == nil {
		return nil
	}
	if strings.TrimSpace(c.Schema) != ContractSchemaV1 {
		return fmt.Errorf("contract: schema must be %q", ContractSchemaV1)
	}
	if strings.TrimSpace(c.WorkItem) == "" {
		return fmt.Errorf("contract: work_item is required")
	}
	if strings.TrimSpace(c.Deliverable) == "" {
		return fmt.Errorf("contract: deliverable is required")
	}
	if strings.TrimSpace(c.Gates) == "" {
		return fmt.Errorf("contract: gates is required")
	}
	for i, scope := range c.Scope {
		if strings.TrimSpace(scope) == "" {
			return fmt.Errorf("contract: scope[%d] must be non-empty", i)
		}
	}
	seen := map[string]bool{}
	for i, criterion := range c.Criteria {
		id := strings.TrimSpace(criterion.ID)
		if id == "" {
			return fmt.Errorf("contract: criteria[%d].id is required", i)
		}
		if !contractClauseIDPattern.MatchString(id) {
			return fmt.Errorf("contract: criteria[%d].id %q must be a stable clause id like AC1", i, criterion.ID)
		}
		if seen[id] {
			return fmt.Errorf("contract: duplicate criterion id %q", id)
		}
		seen[id] = true
		if strings.TrimSpace(criterion.Text) == "" {
			return fmt.Errorf("contract: criteria[%d].text is required", i)
		}
	}
	return nil
}

// CompileContract builds a v1 job contract from existing job metadata and
// ticket/kickoff prose. It returns nil when there is no durable contract floor
// to record, preserving records that have not naturally entered the new path.
func CompileContract(j *Job, opts ContractCompileOptions) *Contract {
	if j == nil {
		return nil
	}
	deliverable := contractDeliverable(j.DeliveryContract)
	if deliverable == "" {
		return nil
	}
	trailer := firstNonEmptyString(opts.RequiredTrailer, requiredTrailerForEpic(opts.ExplicitEpic), extractRequiredTrailer(opts.Text), requiredTrailerForJob(j))
	workItem := firstNonEmptyString(j.TicketURL, j.Ticket, j.ID)
	if workItem == "" {
		return nil
	}
	gates := strings.TrimSpace(opts.Gates)
	if gates == "" {
		gates = ContractDefaultGates
	}
	criteria := extractContractCriteria(opts.Text)
	scope := append([]string(nil), opts.Scope...)
	if len(scope) == 0 {
		scope = extractContractScope(opts.Text)
	}
	return &Contract{
		Schema:      ContractSchemaV1,
		WorkItem:    workItem,
		Deliverable: deliverable,
		Trailer:     trailer,
		Gates:       gates,
		Scope:       scope,
		Criteria:    criteria,
	}
}

// StepDispatchKickoffWithContract combines a job kickoff, durable contract
// rendering, and optional step-specific instructions for runtime dispatch.
func StepDispatchKickoffWithContract(jobKickoff, stepID, instructions string, contract *Contract) string {
	return StepDispatchKickoff(RenderKickoffWithContract(jobKickoff, contract), stepID, instructions)
}

// RenderKickoffWithContract appends or replaces the fixed ## Contract section
// in a kickoff with a rendering sourced from the durable job contract.
func RenderKickoffWithContract(kickoff string, contract *Contract) string {
	section := RenderContractSection(contract)
	if strings.TrimSpace(section) == "" {
		return strings.TrimSpace(kickoff)
	}
	kickoff = strings.TrimSpace(kickoff)
	if kickoff == "" {
		return section
	}
	if replaced, ok := replaceMarkdownSection(kickoff, "Contract", section); ok {
		return strings.TrimSpace(replaced)
	}
	return kickoff + "\n\n" + section
}

// RenderContractSection returns the worker/reviewer-visible fixed contract
// section. Clause ids and the required trailer are intentionally prominent.
func RenderContractSection(contract *Contract) string {
	if contract == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Contract\n\n")
	b.WriteString("Schema: ")
	b.WriteString(firstNonEmptyString(contract.Schema, ContractSchemaV1))
	b.WriteByte('\n')
	if workItem := strings.TrimSpace(contract.WorkItem); workItem != "" {
		b.WriteString("Work item: ")
		b.WriteString(workItem)
		b.WriteByte('\n')
	}
	if deliverable := strings.TrimSpace(contract.Deliverable); deliverable != "" {
		b.WriteString("Deliverable: ")
		b.WriteString(deliverable)
		b.WriteByte('\n')
	}
	if trailer := strings.TrimSpace(contract.Trailer); trailer != "" {
		b.WriteString("Required PR trailer: ")
		b.WriteString(trailer)
		b.WriteByte('\n')
	}
	if gates := strings.TrimSpace(contract.Gates); gates != "" {
		b.WriteString("Gates: ")
		b.WriteString(gates)
		b.WriteByte('\n')
	}
	if len(contract.Scope) > 0 {
		b.WriteString("\nScope:\n")
		for _, scope := range contract.Scope {
			scope = strings.TrimSpace(scope)
			if scope == "" {
				continue
			}
			b.WriteString("- ")
			b.WriteString(scope)
			b.WriteByte('\n')
		}
	}
	if len(contract.Criteria) > 0 {
		b.WriteString("\nCriteria:\n")
		for _, criterion := range contract.Criteria {
			id := strings.TrimSpace(criterion.ID)
			text := strings.TrimSpace(criterion.Text)
			if id == "" || text == "" {
				continue
			}
			b.WriteString("- ")
			b.WriteString(id)
			b.WriteString(": ")
			b.WriteString(text)
			verify := strings.TrimSpace(criterion.Verify)
			if verify == "" {
				verify = ContractDefaultVerify
			}
			b.WriteString("\n  Verify: ")
			b.WriteString(verify)
			b.WriteByte('\n')
		}
	}
	return strings.TrimSpace(b.String())
}

func contractDeliverable(raw string) string {
	raw = strings.TrimSpace(raw)
	lower := strings.ToLower(raw)
	switch {
	case lower == "":
		return ""
	case lower == "ticket_to_pr":
		return "pr"
	case lower == "none":
		return "none"
	case strings.HasPrefix(lower, "report:"):
		return raw
	default:
		return lower
	}
}

func extractContractCriteria(text string) []ContractCriterion {
	sections := candidateCriterionSections(text)
	var out []ContractCriterion
	seen := map[string]bool{}
	for _, section := range sections {
		for _, criterion := range parseCriteriaLines(section.text, section.allowNumbered) {
			if len(out) >= 7 {
				return out
			}
			if seen[criterion.ID] {
				continue
			}
			seen[criterion.ID] = true
			out = append(out, criterion)
		}
	}
	if len(out) > 0 {
		return out
	}
	return nil
}

func candidateCriterionSections(text string) []criterionSection {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	var sections []criterionSection
	for i := 0; i < len(lines); i++ {
		if !isCriterionHeading(lines[i]) {
			continue
		}
		var b strings.Builder
		for j := i + 1; j < len(lines); j++ {
			if isMarkdownHeading(lines[j]) {
				break
			}
			b.WriteString(lines[j])
			b.WriteByte('\n')
		}
		if strings.TrimSpace(b.String()) != "" {
			sections = append(sections, criterionSection{text: b.String(), allowNumbered: true})
		}
	}
	sections = append(sections, criterionSection{text: text, allowNumbered: false})
	return sections
}

func isCriterionHeading(line string) bool {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "#") {
		return false
	}
	heading := strings.TrimSpace(strings.TrimLeft(line, "#"))
	heading = strings.ToLower(heading)
	return heading == "contract" || heading == "criteria" || heading == "acceptance criteria" || heading == "acceptance criteria:" || heading == "acceptance"
}

func isMarkdownHeading(line string) bool {
	return markdownHeadingLevel(line) > 0
}

func markdownHeadingLevel(line string) int {
	line = strings.TrimSpace(line)
	if line == "" || !strings.HasPrefix(line, "#") {
		return 0
	}
	level := 0
	for _, r := range line {
		if r != '#' {
			break
		}
		level++
	}
	return level
}

func parseCriteriaLines(section string, allowNumbered bool) []ContractCriterion {
	var out []ContractCriterion
	nextID := 1
	for _, rawLine := range strings.Split(section, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		if m := explicitCriterionLinePattern.FindStringSubmatch(line); len(m) == 3 {
			id := strings.ToUpper(strings.TrimSpace(m[1]))
			if !allowNumbered && !freeformCriterionIDPattern.MatchString(id) {
				continue
			}
			text, verify := splitVerifyHint(m[2])
			if text == "" {
				continue
			}
			out = append(out, ContractCriterion{ID: id, Text: text, Verify: firstNonEmptyString(verify, ContractDefaultVerify)})
			continue
		}
		if !allowNumbered {
			continue
		}
		if m := numberedCriterionLinePattern.FindStringSubmatch(line); len(m) == 2 {
			text, verify := splitVerifyHint(m[1])
			if text == "" {
				continue
			}
			out = append(out, ContractCriterion{
				ID:     fmt.Sprintf("AC%d", nextID),
				Text:   text,
				Verify: firstNonEmptyString(verify, ContractDefaultVerify),
			})
			nextID++
		}
	}
	return out
}

func splitVerifyHint(raw string) (text, verify string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}
	if m := verifyHintPattern.FindStringSubmatch(raw); len(m) == 2 {
		verify = strings.TrimSpace(m[1])
		raw = strings.TrimSpace(verifyHintPattern.ReplaceAllString(raw, ""))
	}
	return raw, verify
}

func extractContractScope(text string) []string {
	lines := strings.Split(text, "\n")
	inScope := false
	var out []string
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			if inScope && len(out) > 0 {
				break
			}
			continue
		}
		lower := strings.ToLower(strings.TrimSuffix(line, ":"))
		if lower == "likely scope" || lower == "scope" {
			inScope = true
			continue
		}
		if inScope && isMarkdownHeading(line) {
			break
		}
		if !inScope {
			continue
		}
		item := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, "-"), "*"))
		item = strings.Trim(item, "` ")
		if item == "" {
			continue
		}
		out = append(out, item)
		if len(out) >= 10 {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func extractRequiredTrailer(text string) string {
	m := requiredTrailerPattern.FindStringSubmatch(text)
	if len(m) == 0 {
		return ""
	}
	return strings.TrimSpace(firstNonEmptyString(m[1], m[2]))
}

func requiredTrailerForJob(j *Job) string {
	if j == nil {
		return ""
	}
	if ref := githubIssueNumberForExplicitJobEpic(j); ref != "" {
		return "Advances #" + ref
	}
	if strings.TrimSpace(j.TicketURL) != "" {
		if ref := githubIssueNumber(j.TicketURL); ref != "" {
			return "Closes #" + ref
		}
		if isLinearURL(j.TicketURL) {
			return "Closes " + strings.TrimSpace(j.TicketURL)
		}
	}
	if ref := githubIssueNumber(j.Ticket); ref != "" {
		return "Closes #" + ref
	}
	return ""
}

func requiredTrailerForEpic(epic string) string {
	if ref := githubIssueNumber(epic); ref != "" {
		return "Advances #" + ref
	}
	return ""
}

func githubIssueNumberForExplicitJobEpic(j *Job) string {
	if j == nil {
		return ""
	}
	epic := strings.TrimSpace(j.Epic)
	if epic == "" {
		return ""
	}
	if ticketEpic := EpicFromTicketURL(j.TicketURL); ticketEpic != "" && strings.EqualFold(epic, ticketEpic) {
		return ""
	}
	return githubIssueNumber(epic)
}

func githubIssueNumber(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if m := githubIssueRefPattern.FindStringSubmatch(raw); len(m) == 2 {
		return m[1]
	}
	u, err := url.Parse(raw)
	if err != nil || strings.ToLower(strings.TrimPrefix(u.Host, "www.")) != "github.com" {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) >= 4 && strings.EqualFold(parts[2], "issues") {
		return parts[3]
	}
	return ""
}

func isLinearURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && strings.EqualFold(strings.TrimPrefix(u.Host, "www."), "linear.app")
}

func replaceMarkdownSection(markdown, heading, replacement string) (string, bool) {
	lines := strings.Split(markdown, "\n")
	start := -1
	startLevel := 0
	for i, line := range lines {
		level := markdownHeadingLevel(line)
		if level == 0 {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#")), heading) {
			start = i
			startLevel = level
			break
		}
	}
	if start < 0 {
		return markdown, false
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		level := markdownHeadingLevel(lines[i])
		if level > 0 && level <= startLevel {
			end = i
			break
		}
	}
	var out []string
	out = append(out, lines[:start]...)
	out = append(out, strings.Split(strings.TrimSpace(replacement), "\n")...)
	out = append(out, lines[end:]...)
	return strings.TrimSpace(strings.Join(out, "\n")), true
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
