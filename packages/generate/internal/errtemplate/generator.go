package errtemplate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
	"golang.org/x/text/language"
	"golang.org/x/tools/go/packages"

	templategrammar "github.com/wplbyx/modular/packages/internal/errtemplate"
)

const errsImportPath = "github.com/wplbyx/modular/packages/errs"

var reasonPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// Config 描述一次错误语言模板生成或检查任务。
type Config struct {
	Root      string
	Packages  []string
	Output    string
	Languages []string
	Check     bool
}

type definition struct {
	Reason   string
	Pattern  string
	Text     string
	Slots    []string
	Position token.Position
}

// Run 扫描错误定义并生成或检查全部语言文件。
func Run(ctx context.Context, config Config) error {
	config, err := normalizeConfig(config)
	if err != nil {
		return err
	}
	definitions, err := scanDefinitions(ctx, config.Root, config.Packages)
	if err != nil {
		return err
	}

	outputs, err := prepareOutputs(config, definitions)
	if err != nil {
		return err
	}
	if config.Check {
		for _, output := range outputs {
			if output.changed {
				return fmt.Errorf("%s is not synchronized; run err_template_gen without --check", output.filename)
			}
		}
		return nil
	}

	if err := os.MkdirAll(config.Output, 0o755); err != nil {
		return fmt.Errorf("create output directory %q: %w", config.Output, err)
	}
	for _, output := range outputs {
		if !output.changed {
			continue
		}
		if err := writeAtomic(output.filename, output.data, output.mode); err != nil {
			return err
		}
	}
	return nil
}

func normalizeConfig(config Config) (Config, error) {
	if strings.TrimSpace(config.Root) == "" {
		config.Root = "."
	}
	root, err := filepath.Abs(config.Root)
	if err != nil {
		return Config{}, fmt.Errorf("resolve root directory: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return Config{}, fmt.Errorf("stat root directory %q: %w", root, err)
	}
	if !info.IsDir() {
		return Config{}, fmt.Errorf("root %q is not a directory", root)
	}
	config.Root = root
	if len(config.Packages) == 0 {
		config.Packages = []string{"./..."}
	} else {
		config.Packages = append([]string(nil), config.Packages...)
	}
	for index, pattern := range config.Packages {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			return Config{}, errors.New("package pattern is empty")
		}
		config.Packages[index] = pattern
	}
	if strings.TrimSpace(config.Output) == "" {
		return Config{}, errors.New("--out is required")
	}
	if !filepath.IsAbs(config.Output) {
		config.Output = filepath.Join(root, config.Output)
	}
	config.Output = filepath.Clean(config.Output)
	if len(config.Languages) == 0 {
		return Config{}, errors.New("--languages is required")
	}
	config.Languages = append([]string(nil), config.Languages...)
	seenLanguages := make(map[string]struct{}, len(config.Languages))
	for index, value := range config.Languages {
		tag, err := language.Parse(strings.TrimSpace(value))
		if err != nil {
			return Config{}, fmt.Errorf("parse language %q: %w", value, err)
		}
		locale := tag.String()
		if _, ok := seenLanguages[locale]; ok {
			return Config{}, fmt.Errorf("duplicate language %q", locale)
		}
		seenLanguages[locale] = struct{}{}
		config.Languages[index] = locale
	}
	sort.Strings(config.Languages)
	return config, nil
}

func scanDefinitions(ctx context.Context, root string, patterns []string) (map[string]definition, error) {
	fset := token.NewFileSet()
	loaded, err := packages.Load(&packages.Config{
		Context: ctx,
		Dir:     root,
		Fset:    fset,
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports | packages.NeedDeps,
		Tests: false,
	}, patterns...)
	if err != nil {
		return nil, fmt.Errorf("load packages: %w", err)
	}
	var loadErrors []error
	for _, current := range loaded {
		for _, packageErr := range current.Errors {
			loadErrors = append(loadErrors, errors.New(packageErr.Error()))
		}
	}
	if len(loadErrors) > 0 {
		return nil, fmt.Errorf("load packages: %w", errors.Join(loadErrors...))
	}

	found := map[string]definition{
		"UNKNOWN": {
			Reason:  "UNKNOWN",
			Pattern: "request failed",
			Text:    "request failed",
		},
	}
	for _, current := range loaded {
		if current.TypesInfo == nil {
			continue
		}
		for _, file := range current.Syntax {
			ast.Inspect(file, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok || !isObject(callObject(current.TypesInfo, call), "Define", false) {
					return true
				}
				currentDefinition, parseErr := parseDefineCall(fset, current.TypesInfo, call)
				if parseErr != nil {
					loadErrors = append(loadErrors, parseErr)
					return true
				}
				if previous, exists := found[currentDefinition.Reason]; exists {
					if !sameDefinition(previous, currentDefinition) {
						loadErrors = append(loadErrors, fmt.Errorf(
							"%s: conflicting definition for %s; previous definition at %s",
							currentDefinition.Position, currentDefinition.Reason, formatPosition(previous.Position),
						))
					}
					return true
				}
				found[currentDefinition.Reason] = currentDefinition
				return true
			})
		}
	}
	if len(loadErrors) > 0 {
		return nil, errors.Join(loadErrors...)
	}
	return found, nil
}

func parseDefineCall(fset *token.FileSet, info *types.Info, call *ast.CallExpr) (definition, error) {
	position := fset.Position(call.Pos())
	fail := func(format string, args ...any) (definition, error) {
		return definition{}, fmt.Errorf("%s: %s", position, fmt.Sprintf(format, args...))
	}
	if len(call.Args) != 2 {
		return fail("errs.Define requires a reason and errs.Template")
	}
	reason, ok := constantString(info, call.Args[0])
	if !ok {
		return fail("errs.Define reason must be a string constant")
	}
	if !reasonPattern.MatchString(reason) {
		return fail("invalid reason %q", reason)
	}
	templateCall, ok := call.Args[1].(*ast.CallExpr)
	if !ok || !isObject(callObject(info, templateCall), "Template", false) {
		return fail("errs.Define second argument must be a direct errs.Template call")
	}
	if len(templateCall.Args) == 0 {
		return fail("errs.Template requires a pattern")
	}
	pattern, ok := constantString(info, templateCall.Args[0])
	if !ok {
		return fail("errs.Template pattern must be a string constant")
	}
	names := make([]string, 0, len(templateCall.Args)-1)
	for _, expression := range templateCall.Args[1:] {
		if !isNameExpression(info, expression) {
			return fail("errs.Template names must be errs.Name string constants")
		}
		name, ok := constantString(info, expression)
		if !ok {
			return fail("errs.Template names must be errs.Name string constants")
		}
		names = append(names, name)
	}
	parsed, err := templategrammar.FromPattern(pattern, names)
	if err != nil {
		return fail("invalid template for %s: %v", reason, err)
	}
	return definition{
		Reason:   reason,
		Pattern:  pattern,
		Text:     parsed.Text(),
		Slots:    parsed.Slots(),
		Position: position,
	}, nil
}

func callObject(info *types.Info, call *ast.CallExpr) types.Object {
	switch function := call.Fun.(type) {
	case *ast.Ident:
		return info.Uses[function]
	case *ast.SelectorExpr:
		return info.Uses[function.Sel]
	default:
		return nil
	}
}

func isObject(object types.Object, name string, typeName bool) bool {
	if object == nil || object.Name() != name || object.Pkg() == nil || object.Pkg().Path() != errsImportPath {
		return false
	}
	if typeName {
		_, ok := object.(*types.TypeName)
		return ok
	}
	_, ok := object.(*types.Func)
	return ok
}

func isNameExpression(info *types.Info, expression ast.Expr) bool {
	typeOf := info.TypeOf(expression)
	named, ok := typeOf.(*types.Named)
	return ok && isObject(named.Obj(), "Name", true)
}

func constantString(info *types.Info, expression ast.Expr) (string, bool) {
	value := info.Types[expression].Value
	if value == nil || value.Kind() != constant.String {
		return "", false
	}
	return constant.StringVal(value), true
}

func sameDefinition(left, right definition) bool {
	if left.Pattern != right.Pattern || len(left.Slots) != len(right.Slots) {
		return false
	}
	for index := range left.Slots {
		if left.Slots[index] != right.Slots[index] {
			return false
		}
	}
	return true
}

func formatPosition(position token.Position) string {
	if !position.IsValid() {
		return "built-in definition"
	}
	return position.String()
}

type preparedOutput struct {
	filename string
	data     []byte
	mode     fs.FileMode
	changed  bool
}

func prepareOutputs(config Config, definitions map[string]definition) ([]preparedOutput, error) {
	if err := rejectUnexpectedLocaleFiles(config.Output, config.Languages); err != nil {
		return nil, err
	}
	outputs := make([]preparedOutput, 0, len(config.Languages))
	var validationErrors []error
	for _, locale := range config.Languages {
		filename := filepath.Join(config.Output, locale+".yaml")
		output, err := prepareLocale(filename, definitions, config.Check)
		if err != nil {
			validationErrors = append(validationErrors, err)
			continue
		}
		outputs = append(outputs, output)
	}
	if len(validationErrors) > 0 {
		return nil, errors.Join(validationErrors...)
	}
	return outputs, nil
}

func rejectUnexpectedLocaleFiles(directory string, languages []string) error {
	entries, err := os.ReadDir(directory)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read output directory %q: %w", directory, err)
	}
	expected := make(map[string]struct{}, len(languages))
	for _, locale := range languages {
		expected[locale+".yaml"] = struct{}{}
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext != ".yaml" && ext != ".yml" {
			continue
		}
		if _, ok := expected[entry.Name()]; !ok {
			return fmt.Errorf("unexpected locale file %q; add it to --languages or remove it", filepath.Join(directory, entry.Name()))
		}
	}
	return nil
}

func prepareLocale(filename string, definitions map[string]definition, check bool) (preparedOutput, error) {
	existing, err := os.ReadFile(filename)
	missingFile := errors.Is(err, fs.ErrNotExist)
	if err != nil && !missingFile {
		return preparedOutput{}, fmt.Errorf("read locale file %q: %w", filename, err)
	}
	mode := fs.FileMode(0o644)
	if !missingFile {
		if info, statErr := os.Stat(filename); statErr == nil {
			mode = info.Mode().Perm()
		}
	}

	document, mapping, err := decodeDocument(existing, missingFile)
	if err != nil {
		return preparedOutput{}, fmt.Errorf("decode locale file %q: %w", filename, err)
	}
	present := make(map[string]struct{}, len(mapping.Content)/2)
	changed := missingFile
	for index := 0; index < len(mapping.Content); index += 2 {
		key := mapping.Content[index]
		value := mapping.Content[index+1]
		if key.Kind != yaml.ScalarNode || key.Value == "" || !reasonPattern.MatchString(key.Value) {
			return preparedOutput{}, fmt.Errorf("locale file %q contains invalid reason %q", filename, key.Value)
		}
		if _, duplicate := present[key.Value]; duplicate {
			return preparedOutput{}, fmt.Errorf("locale file %q contains duplicate reason %q", filename, key.Value)
		}
		present[key.Value] = struct{}{}
		definition, ok := definitions[key.Value]
		if !ok {
			return preparedOutput{}, fmt.Errorf("locale file %q contains stale reason %q", filename, key.Value)
		}
		if value.Kind != yaml.ScalarNode || value.Tag != "!!str" || value.Value == "" {
			return preparedOutput{}, fmt.Errorf("locale file %q reason %q must have a non-empty string template", filename, key.Value)
		}
		parsed, parseErr := templategrammar.Parse(value.Value)
		if parseErr != nil {
			return preparedOutput{}, fmt.Errorf("locale file %q reason %q: %w", filename, key.Value, parseErr)
		}
		if err := validateSlots(definition.Slots, parsed.Slots()); err != nil {
			return preparedOutput{}, fmt.Errorf("locale file %q reason %q: %w", filename, key.Value, err)
		}
		if ensureSlotComment(key, value, slotComment(definition.Slots)) && !check {
			changed = true
		}
	}

	missing := make([]string, 0)
	for reason := range definitions {
		if _, ok := present[reason]; !ok {
			missing = append(missing, reason)
		}
	}
	sort.Slice(missing, func(i, j int) bool {
		if missing[i] == "UNKNOWN" {
			return true
		}
		if missing[j] == "UNKNOWN" {
			return false
		}
		return missing[i] < missing[j]
	})
	if check && len(missing) > 0 {
		return preparedOutput{}, fmt.Errorf("locale file %q is missing reasons: %s", filename, strings.Join(missing, ", "))
	}
	if !check {
		for _, reason := range missing {
			definition := definitions[reason]
			key := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: reason, HeadComment: slotComment(definition.Slots)}
			value := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: definition.Text, Style: yaml.DoubleQuotedStyle}
			mapping.Content = append(mapping.Content, key, value)
			changed = true
		}
	}

	data, err := encodeDocument(document)
	if err != nil {
		return preparedOutput{}, fmt.Errorf("encode locale file %q: %w", filename, err)
	}
	if !check && !missingFile && bytes.Equal(existing, data) {
		changed = false
	}
	return preparedOutput{filename: filename, data: data, mode: mode, changed: changed}, nil
}

func decodeDocument(data []byte, empty bool) (*yaml.Node, *yaml.Node, error) {
	if empty {
		mapping := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", HeadComment: "Managed by err_template_gen. Edit text freely; keep reason keys and {{.slots}} unchanged."}
		document := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{mapping}}
		return document, mapping, nil
	}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, nil, err
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, nil, errors.New("root must be a YAML mapping")
	}
	return &document, document.Content[0], nil
}

func encodeDocument(document *yaml.Node) ([]byte, error) {
	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(document); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func validateSlots(expected, actual []string) error {
	if len(expected) != len(actual) {
		return fmt.Errorf("slot contract mismatch: expected %v, got %v", expected, actual)
	}
	counts := make(map[string]int, len(expected))
	for _, slot := range expected {
		counts[slot]++
	}
	for _, slot := range actual {
		counts[slot]--
	}
	for _, count := range counts {
		if count != 0 {
			return fmt.Errorf("slot contract mismatch: expected %v, got %v", expected, actual)
		}
	}
	return nil
}

func slotComment(slots []string) string {
	if len(slots) == 0 {
		return "slots: none"
	}
	return "slots: " + strings.Join(slots, ", ")
}

func ensureSlotComment(key, value *yaml.Node, expected string) bool {
	comments := []*string{
		&key.HeadComment,
		&key.LineComment,
		&key.FootComment,
		&value.HeadComment,
		&value.LineComment,
		&value.FootComment,
	}
	found := false
	changed := false
	for _, comment := range comments {
		lines := strings.Split(*comment, "\n")
		for index, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "slots:") {
				found = true
				if trimmed != expected {
					lines[index] = expected
					changed = true
				}
			}
		}
		*comment = strings.Join(lines, "\n")
	}
	if !found {
		if key.HeadComment == "" {
			key.HeadComment = expected
		} else {
			key.HeadComment = expected + "\n" + key.HeadComment
		}
		return true
	}
	return changed
}

func writeAtomic(filename string, data []byte, mode fs.FileMode) error {
	temporary, err := os.CreateTemp(filepath.Dir(filename), ".err-template-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary locale file for %q: %w", filename, err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write temporary locale file for %q: %w", filename, err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync temporary locale file for %q: %w", filename, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary locale file for %q: %w", filename, err)
	}
	if err := os.Chmod(temporaryName, mode); err != nil {
		return fmt.Errorf("set locale file mode for %q: %w", filename, err)
	}
	if err := os.Rename(temporaryName, filename); err != nil {
		return fmt.Errorf("replace locale file %q: %w", filename, err)
	}
	return nil
}
