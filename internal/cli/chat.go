package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Ask answers one user question.
type Ask func(ctx context.Context, question string) (string, error)

// Run reads questions until exit, quit, EOF, or cancellation.
func Run(ctx context.Context, in io.Reader, out, errw io.Writer, ask Ask, progress func(string)) error {
	lines := make(chan string)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(in)
		for scanner.Scan() {
			select {
			case <-ctx.Done():
				return
			case lines <- scanner.Text():
			}
		}
	}()
	for {
		fmt.Fprint(errw, "> ")
		select {
		case <-ctx.Done():
			fmt.Fprintln(errw)
			return nil
		case line, ok := <-lines:
			if !ok {
				fmt.Fprintln(errw)
				return nil
			}
			text := strings.TrimSpace(line)
			if text == "" {
				continue
			}
			if text == "exit" || text == "quit" {
				return nil
			}
			answer, err := ask(ctx, text)
			if err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					fmt.Fprintln(errw)
					return nil
				}
				fmt.Fprintf(errw, "Error: %s\n", err.Error())
				continue
			}
			if progress != nil {
				progress("")
			}
			fmt.Fprintln(out, answer)
		}
	}
}
