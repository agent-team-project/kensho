package loader

import "strings"

// FrontmatterFields contains the scalar, block-scalar, and list values parsed
// from agent frontmatter. We don't try to be a general YAML parser — this is
// the small subset agents actually use.
type FrontmatterFields struct {
	Scalars map[string]string
	Lists   map[string][]string
}

// ParseFrontmatter splits a markdown file with `---`-delimited YAML
// frontmatter and returns the scalar, block-scalar, and list values used by
// agent definitions.
func ParseFrontmatter(text string) (FrontmatterFields, string) {
	empty := FrontmatterFields{Scalars: map[string]string{}, Lists: map[string][]string{}}
	if !strings.HasPrefix(text, "---\n") {
		return empty, text
	}
	endIdx := strings.Index(text[4:], "\n---\n")
	if endIdx == -1 {
		if strings.HasSuffix(text, "\n---") {
			return parseFrontmatterFields(text[4 : len(text)-4]), ""
		}
		return empty, text
	}
	fmText := text[4 : 4+endIdx]
	body := text[4+endIdx+5:]
	return parseFrontmatterFields(fmText), body
}

func parseFrontmatterFields(text string) FrontmatterFields {
	result := FrontmatterFields{
		Scalars: map[string]string{},
		Lists:   map[string][]string{},
	}
	lines := strings.Split(text, "\n")
	i := 0
	for i < len(lines) {
		line := lines[i]
		stripped := strings.TrimSpace(line)
		if stripped == "" || strings.HasPrefix(stripped, "#") {
			i++
			continue
		}
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t' || line[0] == '-') {
			i++
			continue
		}
		colon := strings.IndexByte(line, ':')
		if colon == -1 {
			i++
			continue
		}
		key := strings.TrimSpace(line[:colon])
		val := strings.TrimSpace(line[colon+1:])

		if val == "|" {
			i++
			var blockLines []string
			baseIndent := -1
			for i < len(lines) {
				ln := lines[i]
				if strings.TrimSpace(ln) == "" {
					blockLines = append(blockLines, "")
					i++
					continue
				}
				indent := 0
				for indent < len(ln) && ln[indent] == ' ' {
					indent++
				}
				if baseIndent == -1 {
					if indent == 0 {
						break
					}
					baseIndent = indent
				}
				if indent < baseIndent {
					break
				}
				blockLines = append(blockLines, ln[baseIndent:])
				i++
			}
			result.Scalars[key] = strings.TrimRight(strings.Join(blockLines, "\n"), "\n")
			continue
		}

		// `key:` with no scalar value on the same line — could be a list of
		// `- item` entries on the following lines. Walk forward consuming
		// indented `- ...` lines; if we find at least one, this is a list.
		// Otherwise drop the key; nested mappings are not part of the supported
		// agent-frontmatter subset.
		if val == "" {
			items, consumed := parseListBlock(lines[i+1:])
			if items != nil {
				result.Lists[key] = items
				i += 1 + consumed
				continue
			}
			i++
			continue
		}

		if (strings.HasPrefix(val, `"`) && strings.HasSuffix(val, `"`) && len(val) >= 2) ||
			(strings.HasPrefix(val, `'`) && strings.HasSuffix(val, `'`) && len(val) >= 2) {
			val = val[1 : len(val)-1]
		}
		result.Scalars[key] = val
		i++
	}
	return result
}

// parseListBlock scans forward looking for a YAML-list block: a run of
// indented `- item` lines (single level). Returns the parsed items and the
// number of lines consumed. Returns (nil, 0) if the next non-empty line
// isn't a `- ` item.
//
// Quoted items have their quotes stripped, matching the scalar parser.
func parseListBlock(rest []string) ([]string, int) {
	var items []string
	consumed := 0
	for consumed < len(rest) {
		line := rest[consumed]
		stripped := strings.TrimSpace(line)
		if stripped == "" {
			consumed++
			continue
		}
		// Top-level (unindented) line ends the list — that's the next key.
		if len(line) > 0 && line[0] != ' ' && line[0] != '\t' {
			break
		}
		// Indented but not a `- ...` item → end the list (rare; nested
		// mapping under the key, which we don't support).
		trimmed := strings.TrimLeft(line, " \t")
		if !strings.HasPrefix(trimmed, "- ") && trimmed != "-" {
			break
		}
		raw := strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
		if (strings.HasPrefix(raw, `"`) && strings.HasSuffix(raw, `"`) && len(raw) >= 2) ||
			(strings.HasPrefix(raw, `'`) && strings.HasSuffix(raw, `'`) && len(raw) >= 2) {
			raw = raw[1 : len(raw)-1]
		}
		items = append(items, raw)
		consumed++
	}
	if items == nil {
		return nil, 0
	}
	return items, consumed
}
