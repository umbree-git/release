package web_test

import "testing"

func TestProgressLogWrapsAndShrinks(t *testing.T) {
	c := newConsole(t)
	rules := cssRules(t, c)
	requireDecl(t, rules, ".log li", "overflow-wrap: anywhere")
	requireDecl(t, rules, ".card-head > .inline-progress", "flex-basis: 100%", "min-width: 0")
	requireDecl(t, rules, ".card-head > .confirm-inline", "flex-basis: 100%", "min-width: 0")
	requireDecl(t, rules, ".inline-progress .panel.progress", "min-width: 0")
}

func TestInlineLogMarkersNotClipped(t *testing.T) {
	c := newConsole(t)
	rules := cssRules(t, c)
	requireDecl(t, rules, ".inline-progress .panel.progress", "padding: 0", "overflow: visible")
	requireDecl(t, rules, ".log", "padding-left: 2.5rem")
	requireDecl(t, rules, ".log li", "overflow-wrap: anywhere")
}
