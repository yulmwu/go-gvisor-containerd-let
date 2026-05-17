package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"gopkg.in/yaml.v3"
)

func printAny(w io.Writer, v any, format string) error {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "yaml", "yml":
		b, err := yaml.Marshal(v)
		if err != nil {
			return err
		}

		_, err = fmt.Fprint(w, string(b))
		return err
	default:
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return err
		}

		_, err = fmt.Fprintln(w, string(b))
		return err
	}
}

func printSandboxTable(w io.Writer, items []map[string]string) {
	tw := tabwriter.NewWriter(w, 0, 8, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "NAME\tPHASE\tNODE\tCREATED")
	for _, it := range items {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", it["name"], it["phase"], it["node"], it["created"])
	}

	_ = tw.Flush()
}

func printSandboxTableWide(w io.Writer, items []map[string]string) {
	tw := tabwriter.NewWriter(w, 0, 8, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "NAME\tPHASE\tNODE\tIP\tPORTS\tEGRESS\tCONTAINERS\tEXPIRE_AT\tLAST_ERROR\tCREATED")
	for _, it := range items {
		_, _ = fmt.Fprintf(
			tw,
			"%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			it["name"], it["phase"], it["node"], it["ip"], it["ports"], it["egress"], it["containers"], it["expire_at"], it["last_error"], it["created"],
		)
	}

	_ = tw.Flush()
}

func printNodeTable(w io.Writer, items []map[string]string) {
	tw := tabwriter.NewWriter(w, 0, 8, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "NAME\tSTATE\tIP\tPORT\tUPDATED")
	for _, it := range items {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", it["name"], it["state"], it["ip"], it["port"], it["updated"])
	}

	_ = tw.Flush()
}

func printNodeTableWide(w io.Writer, items []map[string]string) {
	tw := tabwriter.NewWriter(w, 0, 8, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "NAME\tSTATE\tIP\tPORT\tCPU(ALLOC/USED/AVAIL)\tMEM(ALLOC/USED/AVAIL)\tLAST_ERROR\tHEARTBEAT\tUPDATED")
	for _, it := range items {
		_, _ = fmt.Fprintf(
			tw,
			"%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			it["name"], it["state"], it["ip"], it["port"], it["cpu"], it["mem"], it["last_error"], it["last_heartbeat"], it["updated"],
		)
	}

	_ = tw.Flush()
}

func toString(v any) string {
	if v == nil {
		return ""
	}

	return fmt.Sprintf("%v", v)
}

func extractSandboxRows(items any) []map[string]string {
	arr, ok := items.([]any)
	if !ok {
		return nil
	}

	rows := make([]map[string]string, 0, len(arr))
	for _, it := range arr {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}

		if nested, ok := m["sandbox"].(map[string]any); ok {
			m = nested
		}

		status, _ := m["status"].(map[string]any)
		spec, _ := m["spec"].(map[string]any)
		containers := 0
		if cs, ok := spec["containers"].([]any); ok {
			containers = len(cs)
		}
		ports := "-"
		if ap, ok := status["assigned_ports"].([]any); ok {
			parts := make([]string, 0, len(ap))
			for _, p := range ap {
				pm, ok := p.(map[string]any)
				if !ok {
					continue
				}

				proto := strings.ToLower(toString(pm["protocol"]))
				if proto == "" {
					proto = "tcp"
				}

				parts = append(parts, fmt.Sprintf("%s/%s->%s", toString(pm["host_port"]), proto, toString(pm["container_port"])))
			}

			if len(parts) > 0 {
				ports = strings.Join(parts, ",")
			}
		}

		ip := toString(m["ip"])
		if ip == "" {
			ip = toString(status["ip"])
		}

		rows = append(rows, map[string]string{
			"name":       toString(m["id"]),
			"phase":      toString(status["phase"]),
			"node":       toString(status["node_name"]),
			"ip":         ip,
			"ports":      ports,
			"egress":     toString(spec["egress"]),
			"containers": fmt.Sprintf("%d", containers),
			"expire_at":  toString(status["expire_at"]),
			"last_error": toString(status["last_error"]),
			"created":    toString(m["created_at"]),
		})
	}

	sort.Slice(rows, func(i, j int) bool { return rows[i]["name"] < rows[j]["name"] })
	return rows
}

func extractNodeRows(items any) []map[string]string {
	arr, ok := items.([]any)
	if !ok {
		return nil
	}

	rows := make([]map[string]string, 0, len(arr))
	for _, it := range arr {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		res, _ := m["resources"].(map[string]any)
		rows = append(rows, map[string]string{
			"name":           toString(m["name"]),
			"state":          toString(m["state"]),
			"ip":             toString(m["ip"]),
			"port":           toString(m["port"]),
			"updated":        toString(m["updated_at"]),
			"last_error":     toString(m["last_error"]),
			"last_heartbeat": toString(m["last_heartbeat"]),
			"cpu": fmt.Sprintf(
				"%s/%s/%s",
				formatMilli(res["allocatable_cpu_milli"]),
				formatMilli(res["used_cpu_milli"]),
				formatMilli(res["available_cpu_milli"]),
			),
			"mem": fmt.Sprintf(
				"%s/%s/%s",
				formatBytesMB(res["allocatable_memory_bytes"]),
				formatBytesMB(res["used_memory_bytes"]),
				formatBytesMB(res["available_memory_bytes"]),
			),
		})
	}

	sort.Slice(rows, func(i, j int) bool { return rows[i]["name"] < rows[j]["name"] })
	return rows
}

func anyToInt64(v any) int64 {
	switch t := v.(type) {
	case int:
		return int64(t)
	case int8:
		return int64(t)
	case int16:
		return int64(t)
	case int32:
		return int64(t)
	case int64:
		return t
	case uint:
		if uint64(t) > math.MaxInt64 {
			return math.MaxInt64
		}
		return int64(t)
	case uint8:
		return int64(t)
	case uint16:
		return int64(t)
	case uint32:
		return int64(t)
	case uint64:
		if t > math.MaxInt64 {
			return math.MaxInt64
		}
		return int64(t)
	case float32:
		return int64(t)
	case float64:
		return int64(t)
	case json.Number:
		n, _ := t.Int64()
		return n
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return 0
		}

		if i, err := strconv.ParseInt(s, 10, 64); err == nil {
			return i
		}

		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return int64(f)
		}
	}
	return 0
}

func formatMilli(v any) string {
	return fmt.Sprintf("%dm", anyToInt64(v))
}

func formatBytesMB(v any) string {
	b := anyToInt64(v)
	mb := b / (1024 * 1024)
	return fmt.Sprintf("%dMB", mb)
}
