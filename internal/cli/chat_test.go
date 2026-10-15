package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestExitAndQuit(t *testing.T) {
	for _, word := range []string{"exit", "quit"} {
		var out, errw strings.Builder
		err := Run(context.Background(), strings.NewReader(word+"\n"), &out, &errw, func(context.Context, string) (string, error) {
			t.Fatal("ask should not run")
			return "", nil
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if out.Len() != 0 {
			t.Fatalf("output %q", out.String())
		}
	}
}

func TestErrorDoesNotStopSession(t *testing.T) {
	var out, errw strings.Builder
	err := Run(context.Background(), strings.NewReader("bad\nok\nexit\n"), &out, &errw, func(_ context.Context, q string) (string, error) {
		if q == "bad" {
			return "", errors.New("the analytics server is unavailable")
		}
		return "There are 3 trips.", nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errw.String(), "analytics server is unavailable") {
		t.Fatalf("stderr %s", errw.String())
	}
	if strings.TrimSpace(out.String()) != "There are 3 trips." {
		t.Fatalf("stdout %q", out.String())
	}
}

func TestCancelWhileWaiting(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := Run(ctx, strings.NewReader(""), &strings.Builder{}, &strings.Builder{}, func(context.Context, string) (string, error) {
		t.Fatal("ask should not run")
		return "", nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
}
