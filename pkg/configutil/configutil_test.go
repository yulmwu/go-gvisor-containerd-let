package configutil

import (
	"reflect"
	"testing"
)

func TestSplitCSV(t *testing.T) {
	got := SplitCSV(" tcp,udp , icmp ")
	want := []string{"TCP", "UDP", "ICMP"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SplitCSV()=%v want=%v", got, want)
	}
}

func TestCSVEnv(t *testing.T) {
	t.Setenv("X_CSV", "a, b")
	got := CSVEnv("X_CSV", []string{"D"})
	want := []string{"A", "B"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CSVEnv()=%v want=%v", got, want)
	}
}

func TestCSVEnv_Default(t *testing.T) {
	def := []string{"D"}
	got := CSVEnv("NO_CSV", def)
	if !reflect.DeepEqual(got, def) {
		t.Fatalf("CSVEnv()=%v want=%v", got, def)
	}
}
