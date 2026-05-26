package cli

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Choose prints a numbered list of options and reads the user's selection
// from stdin. Returns the zero-based index of the chosen option. If def >= 0,
// pressing Enter without input picks options[def]. The label is shown above
// the options and the default is rendered as "(default: N)" in the prompt.
func Choose(label string, options []string, def int) int {
	fmt.Println()
	fmt.Println(BoldText(CyanText(label)))
	for i, opt := range options {
		marker := " "
		if i == def {
			marker = "*"
		}
		fmt.Printf("  %s %d) %s\n", DimText(marker), i+1, opt)
	}

	reader := bufio.NewReader(os.Stdin)
	for {
		prompt := "Choose [1-" + strconv.Itoa(len(options)) + "]"
		if def >= 0 && def < len(options) {
			prompt += " (default: " + strconv.Itoa(def+1) + ")"
		}
		fmt.Printf("%s: ", prompt)

		line, err := reader.ReadString('\n')
		if err != nil {
			if def >= 0 {
				return def
			}
			fmt.Printf("  %s read input: %v\n", Error("✗"), err)
			os.Exit(1)
		}
		line = strings.TrimSpace(line)
		if line == "" && def >= 0 {
			return def
		}
		n, err := strconv.Atoi(line)
		if err != nil || n < 1 || n > len(options) {
			fmt.Printf("  %s invalid selection, try again\n", Warning("!"))
			continue
		}
		return n - 1
	}
}

// ConfirmYN reads a yes/no answer from stdin. Returns def on empty input.
func ConfirmYN(label string, def bool) bool {
	reader := bufio.NewReader(os.Stdin)
	hint := "[y/N]"
	if def {
		hint = "[Y/n]"
	}
	for {
		fmt.Printf("%s %s: ", label, DimText(hint))
		line, err := reader.ReadString('\n')
		if err != nil {
			return def
		}
		line = strings.TrimSpace(strings.ToLower(line))
		if line == "" {
			return def
		}
		switch line {
		case "y", "yes":
			return true
		case "n", "no":
			return false
		default:
			fmt.Printf("  %s answer with y or n\n", Warning("!"))
		}
	}
}

