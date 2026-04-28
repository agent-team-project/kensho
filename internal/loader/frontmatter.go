package loader

import "strings"

// ParseFrontmatter splits a markdown file with `---`-delimited YAML
// frontmatter into (frontmatter map, body).
//
// Supports the subset of YAML actually used in agent frontmatter:
// scalar values and block scalars (`key: |`). Lists and nested mappings
// are skipped. Behaviourally mirrors the Python loader's `parse_frontmatter`.
func ParseFrontmatter(text string) (map[string]string, string) {
	if !strings.HasPrefix(text, "---\n") {
		return map[string]string{}, text
	}
	endIdx := strings.Index(text[4:], "\n---\n")
	if endIdx == -1 {
		if strings.HasSuffix(text, "\n---") {
			return parseYAMLSubset(text[4 : len(text)-4]), ""
		}
		return map[string]string{}, text
	}
	fmText := text[4 : 4+endIdx]
	body := text[4+endIdx+5:]
	return parseYAMLSubset(fmText), body
}

func parseYAMLSubset(text string) map[string]string {
	result := map[string]string{}
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
			result[key] = strings.TrimRight(strings.Join(blockLines, "\n"), "\n")
			continue
		}

		if (strings.HasPrefix(val, `"`) && strings.HasSuffix(val, `"`) && len(val) >= 2) ||
			(strings.HasPrefix(val, `'`) && strings.HasSuffix(val, `'`) && len(val) >= 2) {
			val = val[1 : len(val)-1]
		}
		result[key] = val
		i++
	}
	return result
}
