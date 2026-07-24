package errtemplate

import (
	"fmt"
	"regexp"
	"strings"
)

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Template 是已经校验的简单具名模板。
type Template struct {
	text     string
	slots    []string
	segments []segment
}

type segment struct {
	text string
	slot string
}

// FromPattern 将代码中的 %v/%% 模式转换为具名模板。
func FromPattern(pattern string, names []string) (Template, error) {
	if pattern == "" {
		return Template{}, fmt.Errorf("template pattern is empty")
	}
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		if !ValidName(name) {
			return Template{}, fmt.Errorf("invalid slot name %q", name)
		}
		if _, ok := seen[name]; ok {
			return Template{}, fmt.Errorf("duplicate slot name %q", name)
		}
		seen[name] = struct{}{}
	}

	var output strings.Builder
	index := 0
	for position := 0; position < len(pattern); position++ {
		if pattern[position] != '%' {
			output.WriteByte(pattern[position])
			continue
		}
		if position+1 >= len(pattern) {
			return Template{}, fmt.Errorf("dangling %% at byte %d", position)
		}
		position++
		switch pattern[position] {
		case '%':
			output.WriteByte('%')
		case 'v':
			if index >= len(names) {
				return Template{}, fmt.Errorf("template contains more %%v slots than names")
			}
			fmt.Fprintf(&output, "{{.%s}}", names[index])
			index++
		default:
			return Template{}, fmt.Errorf("unsupported format verb %%%c at byte %d", pattern[position], position-1)
		}
	}
	if index != len(names) {
		return Template{}, fmt.Errorf("template contains %d %%v slots but %d names were provided", index, len(names))
	}
	return Parse(output.String())
}

// Parse 校验产品可编辑模板，仅接受普通文本和 {{.name}} 槽位。
func Parse(text string) (Template, error) {
	if text == "" {
		return Template{}, fmt.Errorf("template text is empty")
	}
	parsed := Template{text: text}
	position := 0
	for position < len(text) {
		openOffset := strings.Index(text[position:], "{{")
		closeOffset := strings.Index(text[position:], "}}")
		if closeOffset >= 0 && (openOffset < 0 || closeOffset < openOffset) {
			return Template{}, fmt.Errorf("unexpected }} at byte %d", position+closeOffset)
		}
		if openOffset < 0 {
			parsed.segments = appendText(parsed.segments, text[position:])
			break
		}
		open := position + openOffset
		parsed.segments = appendText(parsed.segments, text[position:open])
		closeOffset = strings.Index(text[open+2:], "}}")
		if closeOffset < 0 {
			return Template{}, fmt.Errorf("unclosed template action at byte %d", open)
		}
		close := open + 2 + closeOffset
		action := text[open+2 : close]
		if strings.Contains(action, "{{") || len(action) < 2 || action[0] != '.' {
			return Template{}, fmt.Errorf("template action %q must use {{.name}}", text[open:close+2])
		}
		name := action[1:]
		if !ValidName(name) {
			return Template{}, fmt.Errorf("invalid slot name %q", name)
		}
		parsed.slots = append(parsed.slots, name)
		parsed.segments = append(parsed.segments, segment{slot: name})
		position = close + 2
	}
	if len(parsed.segments) == 0 {
		parsed.segments = []segment{{text: text}}
	}
	return parsed, nil
}

func appendText(segments []segment, text string) []segment {
	if text == "" {
		return segments
	}
	return append(segments, segment{text: text})
}

// ValidName 判断槽位名是否符合固定的小写 snake_case 规范。
func ValidName(name string) bool {
	return namePattern.MatchString(name)
}

func (template Template) Text() string { return template.text }

func (template Template) Slots() []string {
	return append([]string(nil), template.slots...)
}

// Render 渲染模板。缺少的值只替换为 unknown，不破坏其余文案。
func (template Template) Render(values map[string]any, unknown string) (string, []string) {
	var output strings.Builder
	missing := make([]string, 0)
	seenMissing := make(map[string]struct{})
	for _, current := range template.segments {
		if current.slot == "" {
			output.WriteString(current.text)
			continue
		}
		value, ok := values[current.slot]
		if !ok {
			output.WriteString(unknown)
			if _, seen := seenMissing[current.slot]; !seen {
				missing = append(missing, current.slot)
				seenMissing[current.slot] = struct{}{}
			}
			continue
		}
		fmt.Fprint(&output, value)
	}
	return output.String(), missing
}
