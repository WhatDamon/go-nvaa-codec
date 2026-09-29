// Package cli holds the small amount of argument handling the example programs
// share.
//
// It exists so that three examples can offer the same friendly ordering without
// three copies of the same subtlety.
package cli

import (
	"errors"
	"fmt"
	"strings"
)

// SplitOperand pulls the single non-flag operand out of args and returns the
// rest for flag.Parse.
//
// Go's flag package stops parsing at the first operand, so `render FILE -w 80`
// would quietly ignore -w. valueFlags names the flags that consume the next
// argument, which is what makes the operand identifiable. Extracting it first
// lets either ordering work instead of silently doing the wrong thing.
func SplitOperand(args []string, valueFlags map[string]bool) (rest []string, operand string, err error) {
	for index := 0; index < len(args); index++ {
		arg := args[index]

		if strings.HasPrefix(arg, "-") {
			rest = append(rest, arg)

			name := strings.TrimLeft(arg, "-")
			if equals := strings.IndexByte(name, '='); equals >= 0 {
				name = name[:equals]
			}
			// Only a separate value needs carrying over; --flag=value is whole.
			if valueFlags[name] && !strings.ContainsRune(arg, '=') {
				if index+1 >= len(args) {
					return nil, "", fmt.Errorf("flag %s needs a value", arg)
				}
				index++
				rest = append(rest, args[index])
			}
			continue
		}

		if operand != "" {
			return nil, "", fmt.Errorf("unexpected argument %q; one FILE is expected", arg)
		}
		operand = arg
	}

	if operand == "" {
		return nil, "", errors.New("missing FILE argument")
	}
	return rest, operand, nil
}
