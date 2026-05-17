package cmd

import (
	"fmt"
	"strings"

	"sandboxd-o/sandboxd-ctl/client"

	"github.com/spf13/cobra"
)

func newNodeCommand(opts *Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "node",
		Aliases: []string{"nodes", "n"},
		Short:   "Manage orchestrator nodes",
	}

	cmd.AddCommand(newNodeRegisterCommand(opts))
	cmd.AddCommand(newNodeDeleteCommand(opts))
	return cmd
}

func newNodeRegisterCommand(opts *Options) *cobra.Command {
	var (
		name string
		ip   string
		port int
	)

	cmd := &cobra.Command{
		Use:   "register",
		Short: "Register a node endpoint",
		RunE: func(cmd *cobra.Command, args []string) error {
			name = strings.TrimSpace(name)
			ip = strings.TrimSpace(ip)
			if name == "" {
				return fmt.Errorf("--name is required")
			}

			if ip == "" {
				return fmt.Errorf("--ip is required")
			}

			if port <= 0 || port > 65535 {
				return fmt.Errorf("--port must be between 1 and 65535")
			}

			c := mustClient(opts)
			ctx, cancel := withCtx(opts)
			defer cancel()

			out, err := c.RegisterNode(ctx, client.RegisterNodeRequest{
				Name: name,
				IP:   ip,
				Port: port,
			})
			if err != nil {
				return err
			}

			return printAny(cmd.OutOrStdout(), out, opts.Output)
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "node name")
	cmd.Flags().StringVar(&ip, "ip", "", "node IP address")
	cmd.Flags().IntVar(&port, "port", 0, "sandboxd node port")
	return cmd
}

func newNodeDeleteCommand(opts *Options) *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a node registration",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			if name == "" {
				return fmt.Errorf("node name is required")
			}

			c := mustClient(opts)
			ctx, cancel := withCtx(opts)
			defer cancel()

			out, err := c.DeleteNodeWithForce(ctx, name, force)
			if err != nil {
				return err
			}

			return printAny(cmd.OutOrStdout(), out, opts.Output)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "skip node API calls and force-delete node metadata")
	return cmd
}
