package command

import (
	"strings"
	"testing"
	"time"
)

func TestParseCommandsBindsStructFlags(t *testing.T) {
	type serverOptions struct {
		Port uint16 `flag:"port" default:"8080"`
		TLS  bool   `flag:"tls"`
	}
	type options struct {
		Name    string        `flag:"name" short:"n" required:"true"`
		Workers int8          `flag:"workers" default:"2"`
		Timeout time.Duration `flag:"timeout" default:"1s"`
		Labels  []string      `flag:"labels" default:"blue,green"`
		Ratio   float32       `flag:"ratio" default:"1.5"`
		Server  serverOptions `flag:"server"`
	}

	var opts options
	err := ParseCommands([]string{
		"-n", "api",
		"--workers", "4",
		"--timeout", "3s",
		"--labels", "prod,edge",
		"--ratio", "2.25",
		"--server.port", "9090",
		"--server.tls",
	}, &opts)
	if err != nil {
		t.Fatalf("ParseCommands() error = %v", err)
	}

	if opts.Name != "api" ||
		opts.Workers != 4 ||
		opts.Timeout != 3*time.Second ||
		opts.Ratio != 2.25 ||
		opts.Server.Port != 9090 ||
		!opts.Server.TLS {
		t.Fatalf("ParseCommands() opts = %+v", opts)
	}
	if len(opts.Labels) != 2 || opts.Labels[0] != "prod" || opts.Labels[1] != "edge" {
		t.Fatalf("Labels = %#v", opts.Labels)
	}
}

func TestParseCommandsRequiredFlag(t *testing.T) {
	var opts struct {
		Name string `flag:"name" required:"true"`
	}

	err := ParseCommands(nil, &opts)
	if err == nil || !strings.Contains(err.Error(), "required flag") {
		t.Fatalf("ParseCommands() error = %v", err)
	}
}

func TestParseCommandsRequiredFlagMustBeExplicitWithDefault(t *testing.T) {
	var opts struct {
		Name string `flag:"name" default:"api" required:"true"`
	}

	err := ParseCommands(nil, &opts)
	if err == nil || !strings.Contains(err.Error(), "required flag") {
		t.Fatalf("ParseCommands() error = %v", err)
	}
}

func TestParseCommandsRejectsDuplicateFlagNames(t *testing.T) {
	var opts struct {
		Name  string `flag:"name"`
		Alias string `flag:"name"`
	}

	err := ParseCommands(nil, &opts)
	if err == nil || !strings.Contains(err.Error(), "bind flag name") {
		t.Fatalf("ParseCommands() error = %v", err)
	}
}

func TestParseCommandsMapstructureTagOmitempty(t *testing.T) {
	var opts struct {
		ServiceName string `mapstructure:"ServiceName,omitempty"`
	}

	if err := ParseCommands([]string{"--servicename", "orders"}, &opts); err != nil {
		t.Fatalf("ParseCommands() error = %v", err)
	}
	if opts.ServiceName != "orders" {
		t.Fatalf("ServiceName = %q", opts.ServiceName)
	}
}

func TestParseCommandsRejectsInvalidTarget(t *testing.T) {
	err := ParseCommands(nil, struct{}{})
	if err == nil || !strings.Contains(err.Error(), "non-nil struct pointer") {
		t.Fatalf("ParseCommands() value target error = %v", err)
	}

	err = ParseCommands(nil, (*struct{})(nil))
	if err == nil || !strings.Contains(err.Error(), "non-nil struct pointer") {
		t.Fatalf("ParseCommands() nil pointer error = %v", err)
	}
}

func TestParseCommandsUnsupportedType(t *testing.T) {
	var opts struct {
		Err error `flag:"err"`
	}

	err := ParseCommands(nil, &opts)
	if err == nil || !strings.Contains(err.Error(), "unsupported field type") {
		t.Fatalf("ParseCommands() error = %v", err)
	}
}
