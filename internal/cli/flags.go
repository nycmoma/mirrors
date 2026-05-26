package cli

import (
	"errors"
	"fmt"
	"strings"
)

// Command is the normalized CLI request passed to the app layer.
type Command struct {
	Name       string
	Subcommand string

	ConfigPath string
	NameRef    string
	URL        string
	Date       string
	ID         string
	Snapshot   string
	Remove     bool
	Help       bool
}

// FullName returns command plus subcommand for display.
func (c Command) FullName() string {
	if c.Subcommand == "" {
		return c.Name
	}
	return c.Name + " " + c.Subcommand
}

// Parse converts command-line arguments into a normalized Command.
func Parse(args []string) (Command, error) {
	var cmd Command
	if len(args) == 0 {
		return cmd, fmt.Errorf("missing command\n\n%s", HelpText())
	}

	if args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		cmd.Help = true
		return cmd, nil
	}

	cmd.Name = args[0]
	i := 1
	if cmd.Name == "config" {
		if i >= len(args) {
			return cmd, errors.New("missing config subcommand. Valid config commands: generate, validate, show")
		}
		cmd.Subcommand = args[i]
		i++
	}

	for i < len(args) {
		arg := args[i]
		switch arg {
		case "-h", "--help":
			cmd.Help = true
		case "-c", "--config":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return cmd, err
			}
			cmd.ConfigPath = value
			i = next
			continue
		case "-n", "--name":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return cmd, err
			}
			cmd.NameRef = value
			i = next
			continue
		case "-u", "--URL":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return cmd, err
			}
			cmd.URL = value
			i = next
			continue
		case "-d", "--date":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return cmd, err
			}
			cmd.Date = value
			i = next
			continue
		case "-i", "--id":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return cmd, err
			}
			cmd.ID = value
			i = next
			continue
		case "-s", "--snapshot":
			value, next, err := requireValue(args, i, arg)
			if err != nil {
				return cmd, err
			}
			cmd.Snapshot = value
			i = next
			continue
		case "--remove":
			cmd.Remove = true
		default:
			if strings.HasPrefix(arg, "-") {
				return cmd, fmt.Errorf("unknown flag %s", arg)
			}
			return cmd, fmt.Errorf("unexpected argument %q", arg)
		}
		i++
	}

	return cmd, validate(cmd)
}

func requireValue(args []string, index int, flag string) (string, int, error) {
	next := index + 1
	if next >= len(args) || strings.HasPrefix(args[next], "-") {
		return "", index, fmt.Errorf("missing value for %s", flag)
	}
	return args[next], next + 1, nil
}

func validate(cmd Command) error {
	switch cmd.Name {
	case "config":
		return validateConfigCommand(cmd)
	case "create", "fetch", "update", "rollback", "daily", "weekly", "monthly", "hide", "destroy", "cleanup", "list", "info", "more-info":
		return nil
	default:
		return fmt.Errorf("unknown command %q", cmd.Name)
	}
}

func validateConfigCommand(cmd Command) error {
	switch cmd.Subcommand {
	case "generate", "validate", "show":
		return nil
	default:
		return fmt.Errorf("unknown config command %q. Valid config commands: generate, validate, show", cmd.Subcommand)
	}
}
