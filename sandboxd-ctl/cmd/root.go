package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"sandboxd-o/sandboxd-ctl/client"

	"github.com/spf13/cobra"
)

type Options struct {
	Server  string
	Node    string
	Timeout time.Duration
	Output  string
	Limit   int
}

func NewRoot() *cobra.Command {
	opts := &Options{}

	cmd := &cobra.Command{
		Use:           "sbxctl",
		Short:         "sandboxd control client",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(opts.Server) == "" {
				opts.Server = strings.TrimSpace(os.Getenv("SBXCTL_SERVER"))
			}

			if strings.TrimSpace(opts.Server) == "" {
				opts.Server = "http://127.0.0.1:8082"
			}

			if opts.Timeout <= 0 {
				opts.Timeout = 10 * time.Second
			}

			if opts.Limit <= 0 {
				opts.Limit = 100
			}

			return nil
		},
	}

	cmd.PersistentFlags().StringVar(&opts.Server, "server", "", "orchestrator base url (or SBXCTL_SERVER)")
	cmd.PersistentFlags().StringVar(&opts.Node, "node", "", "node id for proxy APIs")
	cmd.PersistentFlags().DurationVar(&opts.Timeout, "timeout", 10*time.Second, "request timeout")
	cmd.PersistentFlags().StringVarP(&opts.Output, "output", "o", "", "output format: json|yaml|wide")
	cmd.PersistentFlags().IntVar(&opts.Limit, "limit", 100, "log/list limit")

	cmd.AddCommand(newGetCommand(opts))
	cmd.AddCommand(newSpecCommand(opts))
	cmd.AddCommand(newCreateCommand(opts))
	cmd.AddCommand(newDeleteCommand(opts))
	cmd.AddCommand(newLogsCommand(opts))

	return cmd
}

func mustClient(opts *Options) *client.Client {
	return client.New(opts.Server, opts.Timeout)
}

func withCtx(opts *Options) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), opts.Timeout)
}

func ensureSandboxResource(name string) error {
	if normalizeResource(name) != "sandbox" {
		return fmt.Errorf("unsupported resource %q (only sandbox is supported now)", name)
	}

	return nil
}
