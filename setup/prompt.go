package setup

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

// IsInteractive returns true if stdin is a terminal.
func IsInteractive() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// confirm prompts the user with a yes/no question. defaultYes controls the
// default when the user just presses enter.
func confirm(prompt string, defaultYes bool) bool {
	suffix := " [Y/n] "
	if !defaultYes {
		suffix = " [y/N] "
	}

	fmt.Print(prompt + suffix)
	reader := bufio.NewReader(os.Stdin)
	answer, _ := reader.ReadString('\n')
	answer = strings.TrimSpace(strings.ToLower(answer))

	if answer == "" {
		return defaultYes
	}
	return answer == "y" || answer == "yes"
}

// promptChoice asks the user to pick from numbered options. Returns the
// 0-based index of their choice.
func promptChoice(prompt string, options []string, defaultIndex int) int {
	fmt.Println(prompt)
	for i, opt := range options {
		marker := "  "
		if i == defaultIndex {
			marker = "* "
		}
		fmt.Printf("  %s%d. %s\n", marker, i+1, opt)
	}

	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Printf("Choice [%d]: ", defaultIndex+1)
		answer, _ := reader.ReadString('\n')
		answer = strings.TrimSpace(answer)

		if answer == "" {
			return defaultIndex
		}

		choice := 0
		if _, err := fmt.Sscanf(answer, "%d", &choice); err == nil && choice >= 1 && choice <= len(options) {
			return choice - 1
		}
		fmt.Println("Invalid choice, please try again.")
	}
}
