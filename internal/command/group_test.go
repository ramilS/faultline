package command

import "testing"

func TestParseGroupArgumentsCreate(t *testing.T) {
	mode, options, valid := ParseGroupArguments([]string{"create", "--name", "backend"})
	if !valid {
		t.Fatal("ParseGroupArguments reported invalid arguments")
	}
	if mode != "group-create" {
		t.Errorf("mode = %q, want %q", mode, "group-create")
	}
	if options.Name != "backend" {
		t.Errorf("name = %q, want %q", options.Name, "backend")
	}
}

// TestParseGroupArgumentsTrimsName pins that the parser trims the name:
// group.Service.Create trims the name before storing it, and
// findGroupByName compares exactly, so a parser that kept the untrimmed
// value would make later lookups (e.g. `group delete --name " backend "`)
// fail to find a group that plainly exists.
func TestParseGroupArgumentsTrimsName(t *testing.T) {
	mode, options, valid := ParseGroupArguments([]string{"create", "--name", " backend "})
	if !valid {
		t.Fatal("ParseGroupArguments reported invalid arguments")
	}
	if mode != "group-create" {
		t.Errorf("mode = %q, want %q", mode, "group-create")
	}
	if options.Name != "backend" {
		t.Errorf("name = %q, want %q", options.Name, "backend")
	}
}

// TestParseGroupArgumentsTrimsNameForChangeCommands covers the "members"
// and "projects" parser, which has its own --name handling separate from
// the create/delete one exercised above.
func TestParseGroupArgumentsTrimsNameForChangeCommands(t *testing.T) {
	mode, options, valid := ParseGroupArguments([]string{"members", "--name", " backend ", "--add", "a@example.com"})
	if !valid {
		t.Fatal("ParseGroupArguments reported invalid arguments")
	}
	if mode != "group-members" {
		t.Errorf("mode = %q, want %q", mode, "group-members")
	}
	if options.Name != "backend" {
		t.Errorf("name = %q, want %q", options.Name, "backend")
	}
}

func TestParseGroupArgumentsRejectsInvalidInput(t *testing.T) {
	cases := map[string][]string{
		"unknown subcommand":     {"promote", "--name", "backend"},
		"members without a name": {"members", "--add", "a@example.com"},
		"members with no change": {"members", "--name", "backend"},
		"members with both":      {"members", "--name", "backend", "--add", "a@example.com", "--remove", "b@example.com"},
		"repeated flag":          {"create", "--name", "backend", "--name", "frontend"},
		"no subcommand":          {},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, valid := ParseGroupArguments(args); valid {
				t.Errorf("ParseGroupArguments(%v) reported valid", args)
			}
		})
	}
}

func TestParseGroupArgumentsList(t *testing.T) {
	mode, _, valid := ParseGroupArguments([]string{"list"})
	if !valid {
		t.Fatal("ParseGroupArguments reported invalid arguments")
	}
	if mode != "group-list" {
		t.Errorf("mode = %q, want %q", mode, "group-list")
	}
}

func TestParseGroupArgumentsDelete(t *testing.T) {
	mode, options, valid := ParseGroupArguments([]string{"delete", "--name", "backend"})
	if !valid {
		t.Fatal("ParseGroupArguments reported invalid arguments")
	}
	if mode != "group-delete" {
		t.Errorf("mode = %q, want %q", mode, "group-delete")
	}
	if options.Name != "backend" {
		t.Errorf("name = %q, want %q", options.Name, "backend")
	}
}

func TestParseGroupArgumentsMembersAdd(t *testing.T) {
	mode, options, valid := ParseGroupArguments([]string{"members", "--name", "backend", "--add", "a@example.com"})
	if !valid {
		t.Fatal("ParseGroupArguments reported invalid arguments")
	}
	if mode != "group-members" {
		t.Errorf("mode = %q, want %q", mode, "group-members")
	}
	if options.Add != "a@example.com" {
		t.Errorf("add = %q, want %q", options.Add, "a@example.com")
	}
}

func TestParseGroupArgumentsMembersRemove(t *testing.T) {
	mode, options, valid := ParseGroupArguments([]string{"members", "--name", "backend", "--remove", "a@example.com"})
	if !valid {
		t.Fatal("ParseGroupArguments reported invalid arguments")
	}
	if mode != "group-members" {
		t.Errorf("mode = %q, want %q", mode, "group-members")
	}
	if options.Remove != "a@example.com" {
		t.Errorf("remove = %q, want %q", options.Remove, "a@example.com")
	}
}

func TestParseGroupArgumentsProjectsAdd(t *testing.T) {
	mode, options, valid := ParseGroupArguments([]string{"projects", "--name", "backend", "--add", "web"})
	if !valid {
		t.Fatal("ParseGroupArguments reported invalid arguments")
	}
	if mode != "group-projects" {
		t.Errorf("mode = %q, want %q", mode, "group-projects")
	}
	if options.Add != "web" {
		t.Errorf("add = %q, want %q", options.Add, "web")
	}
}
