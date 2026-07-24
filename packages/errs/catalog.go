package errs

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
	"golang.org/x/text/language"

	"github.com/wplbyx/modular/packages/internal/errtemplate"
)

type localeCatalog struct {
	tag       language.Tag
	templates map[string]errtemplate.Template
}

// Catalog 是启动时加载、请求期间只读的多语言文案目录。
type Catalog struct {
	defaultLocale string
	locales       map[string]localeCatalog
	tags          []language.Tag
	matcher       language.Matcher
}

// RenderResult 描述本次文案渲染及其回退情况。
type RenderResult struct {
	Message       string
	Locale        string
	Fallback      string
	TemplateError error
}

// LoadCatalog 从 directory 下按 locale 命名的 YAML 文件加载文案。
func LoadCatalog(fsys fs.FS, directory, defaultLocale string) (*Catalog, error) {
	if fsys == nil {
		return nil, errors.New("error catalog filesystem is nil")
	}
	defaultTag, err := language.Parse(defaultLocale)
	if err != nil {
		return nil, fmt.Errorf("parse default locale %q: %w", defaultLocale, err)
	}

	entries, err := fs.ReadDir(fsys, directory)
	if err != nil {
		return nil, fmt.Errorf("read error catalog directory %q: %w", directory, err)
	}
	locales := make(map[string]localeCatalog)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(path.Ext(entry.Name()))
		if ext != ".yaml" && ext != ".yml" {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), path.Ext(entry.Name()))
		tag, err := language.Parse(name)
		if err != nil {
			return nil, fmt.Errorf("parse locale filename %q: %w", entry.Name(), err)
		}
		key := tag.String()
		if _, exists := locales[key]; exists {
			return nil, fmt.Errorf("duplicate locale %q", key)
		}
		loaded, err := loadLocaleFile(fsys, path.Join(directory, entry.Name()), tag)
		if err != nil {
			return nil, err
		}
		locales[key] = loaded
	}
	if len(locales) == 0 {
		return nil, fmt.Errorf("error catalog directory %q contains no YAML files", directory)
	}
	defaultKey := defaultTag.String()
	defaultCatalog, ok := locales[defaultKey]
	if !ok {
		return nil, fmt.Errorf("default locale %q is not present", defaultKey)
	}
	if _, ok := defaultCatalog.templates[UnknownReason]; !ok {
		return nil, fmt.Errorf("default locale %q must define %s", defaultKey, UnknownReason)
	}

	keys := make([]string, 0, len(locales)-1)
	for key := range locales {
		if key != defaultKey {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	tags := []language.Tag{defaultTag}
	for _, key := range keys {
		tags = append(tags, locales[key].tag)
	}
	return &Catalog{
		defaultLocale: defaultKey,
		locales:       locales,
		tags:          tags,
		matcher:       language.NewMatcher(tags),
	}, nil
}

func loadLocaleFile(fsys fs.FS, filename string, tag language.Tag) (localeCatalog, error) {
	data, err := fs.ReadFile(fsys, filename)
	if err != nil {
		return localeCatalog{}, fmt.Errorf("read locale file %q: %w", filename, err)
	}
	var messages map[string]string
	if err := yaml.Unmarshal(data, &messages); err != nil {
		return localeCatalog{}, fmt.Errorf("decode locale file %q: %w", filename, err)
	}
	templates := make(map[string]errtemplate.Template, len(messages))
	for reason, text := range messages {
		reason = strings.TrimSpace(reason)
		if reason == "" || text == "" {
			return localeCatalog{}, fmt.Errorf("locale file %q contains an empty reason or template", filename)
		}
		if !reasonPattern.MatchString(reason) {
			return localeCatalog{}, fmt.Errorf("locale file %q contains invalid reason %q", filename, reason)
		}
		parsed, err := errtemplate.Parse(text)
		if err != nil {
			return localeCatalog{}, fmt.Errorf("parse template %s in %q: %w", reason, filename, err)
		}
		templates[reason] = parsed
	}
	return localeCatalog{tag: tag, templates: templates}, nil
}

// Render 根据 Accept-Language 和 Message 渲染文案。
func (catalog *Catalog) Render(acceptLanguage string, message Message) RenderResult {
	message = normalizeMessage(message)
	requested := catalog.defaultLocale
	if acceptLanguage != "" {
		_, index := language.MatchStrings(catalog.matcher, acceptLanguage)
		if index >= 0 && index < len(catalog.tags) {
			requested = catalog.tags[index].String()
		}
	}

	type candidate struct {
		locale   string
		reason   string
		fallback string
	}
	candidates := []candidate{
		{requested, message.reason, "none"},
		{catalog.defaultLocale, message.reason, "default_locale"},
		{requested, UnknownReason, "requested_unknown"},
		{catalog.defaultLocale, UnknownReason, "default_unknown"},
	}
	seen := make(map[string]struct{}, len(candidates))
	for _, current := range candidates {
		key := current.locale + "\x00" + current.reason
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		locale, ok := catalog.locales[current.locale]
		if !ok {
			continue
		}
		parsed, ok := locale.templates[current.reason]
		if !ok {
			continue
		}

		output, missing, extra := message.render(parsed)
		var renderErr error
		if current.reason == message.reason {
			if err := validateSlotContract(message.slots(), parsed.Slots()); err != nil {
				renderErr = errors.Join(renderErr, err)
			}
		}
		if len(missing) > 0 {
			renderErr = errors.Join(renderErr, fmt.Errorf("missing template values: %s", joinNames(missing)))
		}
		if len(extra) > 0 {
			renderErr = errors.Join(renderErr, fmt.Errorf("undeclared template values: %s", joinNames(extra)))
		}
		return RenderResult{
			Message:       output,
			Locale:        current.locale,
			Fallback:      current.fallback,
			TemplateError: renderErr,
		}
	}
	return RenderResult{
		Message:  unknownMessageFallback(),
		Locale:   catalog.defaultLocale,
		Fallback: "builtin",
	}
}

func validateSlotContract(expected, actual []string) error {
	expectedCounts := countSlots(expected)
	actualCounts := countSlots(actual)
	if len(expected) == len(actual) && equalSlotCounts(expectedCounts, actualCounts) {
		return nil
	}
	return fmt.Errorf("template slot contract mismatch: expected %v, got %v", expected, actual)
}

func countSlots(slots []string) map[string]int {
	counts := make(map[string]int, len(slots))
	for _, slot := range slots {
		counts[slot]++
	}
	return counts
}

func equalSlotCounts(left, right map[string]int) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

func joinNames(names []Name) string {
	values := make([]string, len(names))
	for index, name := range names {
		values[index] = string(name)
	}
	return strings.Join(values, ", ")
}
