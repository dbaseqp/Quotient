package api

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTimelapseOrderMatchesGraphs(t *testing.T) {
	// The order JavaScript's localeCompare gives, as /graphs sorts teams and services.
	want := []string{"a b", "a.b", "alpha", "apache", "Bind", "dns", "DNS", "é", "ecole", "école",
		"mail-smtp", "team02", "team10", "team2", "web", "Web", "WEB", "web_http", "web-http",
		"web2", "webhttp", "x_y", "x-y", "x.y", "x1", "Zeta"}
	got := slices.Clone(want)
	slices.Reverse(got)
	slices.SortFunc(got, labelOrder())
	assert.Equal(t, want, got)
}
