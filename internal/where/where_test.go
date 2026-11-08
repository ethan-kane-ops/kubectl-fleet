package where

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	cases := []struct {
		exprs      []string
		wantErr    string
		needsNodes bool
		reach      Reach
	}{
		{exprs: nil, reach: ReachAny},
		{exprs: []string{"version<1.30"}, reach: ReachAny},
		{exprs: []string{"version>=1.29.3", "nodes>50"}, needsNodes: true, reach: ReachAny},
		{exprs: []string{" nodes != 3 "}, needsNodes: true, reach: ReachAny},
		{exprs: []string{"reachable"}, reach: ReachOnly},
		{exprs: []string{"!reachable"}, reach: UnreachableOnly},
		{exprs: []string{"version<abc"}, wantErr: `invalid version "abc"`},
		{exprs: []string{"nodes>many"}, wantErr: `invalid node count "many"`},
		{exprs: []string{"foo=1"}, wantErr: `unknown attribute "foo"`},
		{exprs: []string{"version"}, wantErr: "needs an operator"},
		{exprs: []string{"reachable", "!reachable"}, wantErr: "both reachable and !reachable"},
		{exprs: []string{"!reachable", "version<1.30"}, wantErr: "!reachable cannot be combined"},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.exprs, " "), func(t *testing.T) {
			f, err := Parse(tc.exprs)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if f.NeedsNodes() != tc.needsNodes || f.Reachability() != tc.reach {
				t.Errorf("needsNodes=%v reach=%v", f.NeedsNodes(), f.Reachability())
			}
		})
	}
}

func TestMatchVersionAtTypedPrecision(t *testing.T) {
	cases := []struct {
		expr, server string
		want         bool
	}{
		{"version<1.30", "v1.29.8-eks-a737599", true},
		{"version<=1.30", "v1.30.2+k3s1", true},
		{"version=1.30", "v1.30.4", true},
		{"version!=1.30", "v1.30.4", false},
		{"version<1.30", "v1.31.0", false},
		{"version>1.29", "v1.30.0", true},
		{"version>1.29", "v1.29.9", false},
		{"version>=1.29.3", "v1.29.3-gke.100", true},
		{"version<1.29.3", "v1.29.10", false},
		{"version>=1.30", "garbage", false},
	}
	for _, tc := range cases {
		t.Run(tc.expr+" "+tc.server, func(t *testing.T) {
			f, err := Parse([]string{tc.expr})
			if err != nil {
				t.Fatal(err)
			}
			if got := f.Match(Attributes{Version: tc.server}); got != tc.want {
				t.Errorf("Match=%v want %v", got, tc.want)
			}
		})
	}
}

func TestMatchNodesAndCombined(t *testing.T) {
	f, err := Parse([]string{"nodes>50", "version<1.30"})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		a    Attributes
		want bool
	}{
		{Attributes{Version: "v1.29.1", Nodes: 51}, true},
		{Attributes{Version: "v1.29.1", Nodes: 50}, false},
		{Attributes{Version: "v1.30.0", Nodes: 200}, false},
	}
	for _, tc := range cases {
		if got := f.Match(tc.a); got != tc.want {
			t.Errorf("Match(%+v)=%v want %v", tc.a, got, tc.want)
		}
	}
}
