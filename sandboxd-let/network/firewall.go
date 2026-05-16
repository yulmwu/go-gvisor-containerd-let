package network

import (
	"bufio"
	"fmt"
	"hash/fnv"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/coreos/go-iptables/iptables"
)

var debugLogger = func(string, ...any) {}

func SetDebugLogger(fn func(string, ...any)) {
	if fn == nil {
		debugLogger = func(string, ...any) {}
		return
	}

	debugLogger = fn
}

func dbg(format string, args ...any) {
	debugLogger(format, args...)
}

func iptCmd(table, op, chain string, args ...string) string {
	parts := []string{"iptables", "-t", table, op, chain}
	for _, a := range args {
		if strings.ContainsAny(a, " \t\"") {
			parts = append(parts, strconv.Quote(a))
		} else {
			parts = append(parts, a)
		}
	}

	return strings.Join(parts, " ")
}

func dbgIpt(table, op, chain string, args ...string) {
	dbg("iptables-cmd: %s", iptCmd(table, op, chain, args...))
}

type PublishedPort struct {
	ContainerPort int
	Protocol      string
}

type HostPortForward struct {
	HostPort      int
	ContainerPort int
	Protocol      string
}

const hostPortCommentPrefix = "SBX:"

// EnsureGlobalChains installs global jump points for sandbox firewalling.
func EnsureGlobalChains(ipt *iptables.IPTables, forwardHookChains []string) error {
	dbg("iptables: ensure global chains forward_hooks=%v", forwardHookChains)
	dbgIpt("filter", "-N", "SANDBOX-FWD")
	_ = ipt.NewChain("filter", "SANDBOX-FWD")
	dbgIpt("filter", "-N", "SANDBOX-IN")
	_ = ipt.NewChain("filter", "SANDBOX-IN")

	if err := insertFirst(ipt, "FORWARD", []string{"-m", "mark", "--mark", "0x2000/0x2000", "-j", "ACCEPT"}); err != nil {
		return err
	}

	if err := insertFirst(ipt, "FORWARD", []string{"-m", "conntrack", "--ctstate", "ESTABLISHED,RELATED", "-j", "ACCEPT"}); err != nil {
		return err
	}

	if len(forwardHookChains) == 0 {
		forwardHookChains = []string{"FORWARD"}
	}

	if !containsChain(forwardHookChains, "FORWARD") {
		forwardHookChains = append([]string{"FORWARD"}, forwardHookChains...)
	}

	for _, chain := range forwardHookChains {
		chain = strings.TrimSpace(strings.ToUpper(chain))
		if chain == "" || !hasChain(ipt, chain) {
			continue
		}

		if err := insertFirst(ipt, chain, []string{"-j", "SANDBOX-FWD"}); err != nil {
			return err
		}
	}

	if err := insertFirst(ipt, "INPUT", []string{"-j", "SANDBOX-IN"}); err != nil {
		return err
	}

	return nil
}

// ApplySandboxRules creates per-sandbox chains and attaches them to global chains.
func ApplySandboxRules(ipt *iptables.IPTables, sandboxID, ip, bridgeCIDR, bridgeIF string, egress bool, ports []PublishedPort) error {
	dbg("iptables: apply sandbox rules sandbox=%s ip=%s bridge_if=%s egress=%t ports=%v", sandboxID, ip, bridgeIF, egress, ports)
	short := shortID(sandboxID)
	fwd := "SBX-" + short + "-FWD"
	in := "SBX-" + short + "-IN"

	dbgIpt("filter", "-N", fwd)
	_ = ipt.NewChain("filter", fwd)
	dbgIpt("filter", "-N", in)
	_ = ipt.NewChain("filter", in)

	dbgIpt("filter", "-A", "SANDBOX-FWD", "-s", ip+"/32", "-j", fwd)
	if err := ipt.AppendUnique("filter", "SANDBOX-FWD", "-s", ip+"/32", "-j", fwd); err != nil {
		return err
	}

	dbgIpt("filter", "-A", "SANDBOX-IN", "-i", bridgeIF, "-s", ip+"/32", "-j", in)
	if err := ipt.AppendUnique("filter", "SANDBOX-IN", "-i", bridgeIF, "-s", ip+"/32", "-j", in); err != nil {
		return err
	}

	for _, p := range ports {
		proto := strings.ToLower(p.Protocol)
		if proto == "" {
			proto = "tcp"
		}
		_ = insertForwardAcceptEarly(ipt, ip, proto, p.ContainerPort)
	}

	_ = ipt.AppendUnique("filter", fwd, "-m", "conntrack", "--ctstate", "ESTABLISHED,RELATED", "-j", "ACCEPT")
	if egress {
		for _, ns := range systemNameservers() {
			_ = ipt.AppendUnique("filter", fwd, "-d", ns, "-p", "udp", "--dport", "53", "-j", "ACCEPT")
			_ = ipt.AppendUnique("filter", fwd, "-d", ns, "-p", "tcp", "--dport", "53", "-j", "ACCEPT")
		}

		_ = ipt.AppendUnique("filter", fwd, "-p", "udp", "--dport", "53", "-j", "ACCEPT")
		_ = ipt.AppendUnique("filter", fwd, "-p", "tcp", "--dport", "53", "-j", "ACCEPT")
	}

	_ = ipt.AppendUnique("filter", fwd, "-m", "addrtype", "--dst-type", "LOCAL", "-j", "REJECT")
	for _, cidr := range []string{
		bridgeCIDR,
		"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
		"127.0.0.0/8", "169.254.0.0/16", "100.64.0.0/10",
		"198.18.0.0/15", "224.0.0.0/4", "240.0.0.0/4",
	} {
		_ = ipt.AppendUnique("filter", fwd, "-d", cidr, "-j", "REJECT")
	}

	if egress {
		_ = ipt.AppendUnique("filter", fwd, "-j", "ACCEPT")
	} else {
		_ = ipt.AppendUnique("filter", fwd, "-j", "REJECT")
	}

	_ = ipt.AppendUnique("filter", in, "-m", "conntrack", "--ctstate", "ESTABLISHED,RELATED", "-j", "ACCEPT")
	for _, p := range ports {
		proto := strings.ToLower(p.Protocol)
		if proto == "" {
			proto = "tcp"
		}

		_ = ipt.AppendUnique("filter", in, "-p", proto, "--sport", fmt.Sprintf("%d", p.ContainerPort), "-j", "ACCEPT")
	}

	_ = ipt.AppendUnique("filter", in, "-j", "REJECT")

	return nil
}

// DeleteSandboxRules removes per-sandbox hooks and chains.
func DeleteSandboxRules(ipt *iptables.IPTables, sandboxID, ip, bridgeIF string, ports []PublishedPort) {
	dbg("iptables: delete sandbox rules sandbox=%s ip=%s bridge_if=%s ports=%v", sandboxID, ip, bridgeIF, ports)
	short := shortID(sandboxID)
	fwd := "SBX-" + short + "-FWD"
	in := "SBX-" + short + "-IN"

	for _, p := range ports {
		proto := strings.ToLower(p.Protocol)
		if proto == "" {
			proto = "tcp"
		}

		_ = ipt.Delete("filter", "FORWARD", "-d", ip+"/32", "-p", proto, "--dport", fmt.Sprintf("%d", p.ContainerPort), "-j", "ACCEPT")
	}

	dbgIpt("filter", "-D", "SANDBOX-FWD", "-s", ip+"/32", "-j", fwd)
	_ = ipt.Delete("filter", "SANDBOX-FWD", "-s", ip+"/32", "-j", fwd)
	dbgIpt("filter", "-D", "SANDBOX-IN", "-i", bridgeIF, "-s", ip+"/32", "-j", in)
	_ = ipt.Delete("filter", "SANDBOX-IN", "-i", bridgeIF, "-s", ip+"/32", "-j", in)
	dbgIpt("filter", "-F", fwd)
	_ = ipt.ClearChain("filter", fwd)
	dbgIpt("filter", "-F", in)
	_ = ipt.ClearChain("filter", in)
	dbgIpt("filter", "-X", fwd)
	_ = ipt.DeleteChain("filter", fwd)
	dbgIpt("filter", "-X", in)
	_ = ipt.DeleteChain("filter", in)
}

// ApplyHostPortDNAT publishes hostPort to sandboxIP:containerPort for TCP/UDP.
func ApplyHostPortDNAT(ipt *iptables.IPTables, sandboxID string, sandboxIP string, forwards []HostPortForward) error {
	dbg("iptables: apply DNAT sandbox=%s ip=%s forwards=%v", sandboxID, sandboxIP, forwards)
	for _, f := range forwards {
		proto := strings.ToLower(strings.TrimSpace(f.Protocol))
		if proto == "" {
			proto = "tcp"
		}

		target := fmt.Sprintf("%s:%d", sandboxIP, f.ContainerPort)
		comment := hostPortCommentPrefix + sandboxID
		pr := []string{"-p", proto, "--dport", fmt.Sprintf("%d", f.HostPort), "-m", "comment", "--comment", comment, "-j", "DNAT", "--to-destination", target}
		out := []string{"-m", "addrtype", "--dst-type", "LOCAL", "-p", proto, "--dport", fmt.Sprintf("%d", f.HostPort), "-m", "comment", "--comment", comment, "-j", "DNAT", "--to-destination", target}

		dbgIpt("nat", "-A", "PREROUTING", pr...)
		if err := ipt.AppendUnique("nat", "PREROUTING", pr...); err != nil {
			return err
		}

		dbgIpt("nat", "-A", "OUTPUT", out...)
		if err := ipt.AppendUnique("nat", "OUTPUT", out...); err != nil {
			return err
		}
	}

	return nil
}

// DeleteHostPortDNAT removes hostPort publish rules.
func DeleteHostPortDNAT(ipt *iptables.IPTables, sandboxID string, sandboxIP string, forwards []HostPortForward) {
	dbg("iptables: delete DNAT sandbox=%s ip=%s forwards=%v", sandboxID, sandboxIP, forwards)
	for _, f := range forwards {
		proto := strings.ToLower(strings.TrimSpace(f.Protocol))
		if proto == "" {
			proto = "tcp"
		}

		target := fmt.Sprintf("%s:%d", sandboxIP, f.ContainerPort)
		comment := hostPortCommentPrefix + sandboxID

		dbgIpt("nat", "-D", "PREROUTING", "-p", proto, "--dport", fmt.Sprintf("%d", f.HostPort), "-m", "comment", "--comment", comment, "-j", "DNAT", "--to-destination", target)
		_ = ipt.Delete("nat", "PREROUTING", "-p", proto, "--dport", fmt.Sprintf("%d", f.HostPort), "-m", "comment", "--comment", comment, "-j", "DNAT", "--to-destination", target)

		dbgIpt("nat", "-D", "OUTPUT", "-m", "addrtype", "--dst-type", "LOCAL", "-p", proto, "--dport", fmt.Sprintf("%d", f.HostPort), "-m", "comment", "--comment", comment, "-j", "DNAT", "--to-destination", target)
		_ = ipt.Delete("nat", "OUTPUT", "-m", "addrtype", "--dst-type", "LOCAL", "-p", proto, "--dport", fmt.Sprintf("%d", f.HostPort), "-m", "comment", "--comment", comment, "-j", "DNAT", "--to-destination", target)
	}
}

// DeleteHostPortDNATBySandbox removes every DNAT publish rule tagged for a sandbox.
func DeleteHostPortDNATBySandbox(ipt *iptables.IPTables, sandboxID string) {
	dbg("iptables: delete DNAT by sandbox tag sandbox=%s", sandboxID)
	tag := "--comment " + hostPortCommentPrefix + sandboxID
	deleteTagged := func(chain string) {
		rules, err := ipt.List("nat", chain)
		if err != nil {
			return
		}

		for _, line := range rules {
			if !strings.Contains(line, tag) {
				continue
			}

			fields := strings.Fields(strings.TrimSpace(line))
			if len(fields) < 3 || fields[0] != "-A" {
				continue
			}

			_ = ipt.Delete("nat", chain, fields[2:]...)
			dbgIpt("nat", "-D", chain, fields[2:]...)
		}
	}

	deleteTagged("PREROUTING")
	deleteTagged("OUTPUT")
}

// DeleteOrphanHostPortDNAT removes tagged hostPort DNAT rules whose sandbox IDs are not in keep.
func DeleteOrphanHostPortDNAT(ipt *iptables.IPTables, keep map[string]struct{}) {
	dbg("iptables: delete orphan DNAT keep_count=%d", len(keep))
	cleanup := func(chain string) {
		rules, err := ipt.List("nat", chain)
		if err != nil {
			return
		}

		for _, line := range rules {
			fields := strings.Fields(strings.TrimSpace(line))
			if len(fields) < 3 || fields[0] != "-A" {
				continue
			}

			id := sandboxIDFromRule(fields)
			if id == "" {
				continue
			}

			if _, ok := keep[id]; ok {
				continue
			}

			_ = ipt.Delete("nat", chain, fields[2:]...)
			dbgIpt("nat", "-D", chain, fields[2:]...)
		}
	}

	cleanup("PREROUTING")
	cleanup("OUTPUT")
}

func sandboxIDFromRule(fields []string) string {
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] != "--comment" {
			continue
		}

		v := strings.Trim(fields[i+1], "\"")
		if after, ok := strings.CutPrefix(v, hostPortCommentPrefix); ok {
			return after
		}
	}

	return ""
}

func insertForwardAcceptEarly(ipt *iptables.IPTables, ip, proto string, dport int) error {
	rule := []string{"-d", ip + "/32", "-p", proto, "--dport", fmt.Sprintf("%d", dport), "-j", "ACCEPT"}
	dbgIpt("filter", "-D", "FORWARD", rule...)

	_ = ipt.Delete("filter", "FORWARD", rule...)
	dbgIpt("filter", "-I", "FORWARD", append([]string{"2"}, rule...)...)
	if err := ipt.Insert("filter", "FORWARD", 2, rule...); err != nil {
		return err
	}

	return nil
}

func insertFirst(ipt *iptables.IPTables, parent string, rule []string) error {
	dbgIpt("filter", "-D", parent, rule...)
	_ = ipt.Delete("filter", parent, rule...)

	dbgIpt("filter", "-I", parent, append([]string{"1"}, rule...)...)
	if err := ipt.Insert("filter", parent, 1, rule...); err != nil {
		return fmt.Errorf("insert rule: %w", err)
	}

	return nil
}

func shortID(id string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))

	return fmt.Sprintf("%08x", h.Sum32())
}

func hasChain(ipt *iptables.IPTables, chain string) bool {
	chains, err := ipt.ListChains("filter")
	if err != nil {
		return false
	}

	return slices.Contains(chains, chain)
}

func containsChain(chains []string, target string) bool {
	target = strings.ToUpper(strings.TrimSpace(target))
	for _, c := range chains {
		if strings.ToUpper(strings.TrimSpace(c)) == target {
			return true
		}
	}

	return false
}

func systemNameservers() []string {
	path := "/etc/resolv.conf"
	if st, err := os.Stat("/run/systemd/resolve/resolv.conf"); err == nil && !st.IsDir() {
		path = "/run/systemd/resolve/resolv.conf"
	}

	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var out []string
	seen := map[string]struct{}{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "nameserver" {
			continue
		}

		ip := net.ParseIP(fields[1])
		if ip == nil || ip.To4() == nil {
			continue
		}

		s := ip.String() + "/32"
		if _, ok := seen[s]; ok {
			continue
		}

		seen[s] = struct{}{}
		out = append(out, s)
	}

	return out
}
