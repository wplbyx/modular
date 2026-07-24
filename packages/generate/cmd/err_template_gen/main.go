package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/wplbyx/modular/packages/generate/internal/errtemplate"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "err_template_gen:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("err_template_gen", flag.ContinueOnError)
	root := flags.String("root", ".", "Go module root")
	packageValues := flags.String("packages", "./...", "comma-separated Go package patterns")
	output := flags.String("out", "", "locale YAML output directory")
	languageValues := flags.String("languages", "", "comma-separated BCP 47 locales")
	check := flags.Bool("check", false, "validate without writing files")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %s", strings.Join(flags.Args(), " "))
	}
	return errtemplate.Run(ctx, errtemplate.Config{
		Root:      *root,
		Packages:  splitCSV(*packageValues),
		Output:    *output,
		Languages: splitCSV(*languageValues),
		Check:     *check,
	})
}

func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		result = append(result, strings.TrimSpace(part))
	}
	return result
}
