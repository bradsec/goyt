package main

import (
	"fmt"
	"os"
	"strings"
)

// printBanner selects the best ANSI banner variant for the current terminal.
// Precedence: NO_COLOR / dumb terminal -> plain, truecolor -> 24-bit,
// 256color -> 256-palette, otherwise the 16-color fallback.
func printBanner() {
	_, noColor := os.LookupEnv("NO_COLOR")
	term := os.Getenv("TERM")
	colorterm := os.Getenv("COLORTERM")

	switch {
	case noColor || term == "dumb":
		printPlainBanner()
	case colorterm == "truecolor" || colorterm == "24bit" || strings.Contains(term, "256color"):
		print256Banner()
	default:
		print16Banner()
	}
}

func print256Banner() {
	fmt.Println("\x1b[38;5;235m\x1b[49m                                         \x1b[0m")
	fmt.Println("\x1b[38;5;235m\x1b[49m \x1b[38;5;75m\x1b[49m ▐\x1b[38;5;69m\x1b[48;5;26m▄▄▄▄▄▄▄\x1b[38;5;69m\x1b[49m▌\x1b[38;5;75m\x1b[49m ▐\x1b[38;5;69m\x1b[48;5;26m▄▄▄▄▄▄\x1b[38;5;69m\x1b[49m▌\x1b[38;5;75m\x1b[49m ▐\x1b[38;5;69m\x1b[48;5;26m▄▄\x1b[38;5;69m\x1b[49m▌\x1b[38;5;75m\x1b[49m  ▐\x1b[38;5;69m\x1b[48;5;26m▄▄\x1b[38;5;69m\x1b[49m▌\x1b[38;5;75m\x1b[49m▐\x1b[38;5;69m\x1b[48;5;26m▄▄▄▄▄▄▄▄\x1b[38;5;69m\x1b[49m▌\x1b[0m")
	fmt.Println("\x1b[38;5;235m\x1b[49m \x1b[38;5;75m\x1b[49m▐\x1b[38;5;69m\x1b[48;5;26m██\x1b[38;5;69m\x1b[49m▌\x1b[38;5;75m\x1b[49m      ▐\x1b[38;5;69m\x1b[48;5;26m██\x1b[38;5;69m\x1b[49m▌\x1b[38;5;75m\x1b[49m  ▐\x1b[38;5;69m\x1b[48;5;26m██\x1b[38;5;69m\x1b[49m▌\x1b[38;5;75m\x1b[49m▐\x1b[38;5;69m\x1b[48;5;26m██\x1b[38;5;69m\x1b[49m▌\x1b[38;5;75m\x1b[49m  ▐\x1b[38;5;69m\x1b[48;5;26m██\x1b[38;5;69m\x1b[49m▌\x1b[38;5;75m\x1b[49m   ▐\x1b[38;5;69m\x1b[48;5;26m██\x1b[38;5;69m\x1b[49m▌\x1b[38;5;75m\x1b[49m   \x1b[0m")
	fmt.Println("\x1b[38;5;235m\x1b[49m \x1b[38;5;75m\x1b[49m▐\x1b[38;5;69m\x1b[48;5;26m██\x1b[38;5;69m\x1b[49m▌\x1b[38;5;75m\x1b[49m▐\x1b[38;5;69m\x1b[49m██\x1b[38;5;69m\x1b[48;5;26m██\x1b[38;5;69m\x1b[49m▌\x1b[38;5;75m\x1b[49m▐\x1b[38;5;69m\x1b[48;5;26m██\x1b[38;5;69m\x1b[49m▌\x1b[38;5;75m\x1b[49m  ▐\x1b[38;5;69m\x1b[48;5;26m██\x1b[38;5;69m\x1b[49m▌\x1b[38;5;75m\x1b[49m ▐\x1b[38;5;69m\x1b[48;5;26m███████\x1b[38;5;69m\x1b[49m▌\x1b[38;5;75m\x1b[49m   ▐\x1b[38;5;69m\x1b[48;5;26m██\x1b[38;5;69m\x1b[49m▌\x1b[38;5;75m\x1b[49m   \x1b[0m")
	fmt.Println("\x1b[38;5;235m\x1b[49m \x1b[38;5;69m\x1b[49m▐\x1b[38;5;75m\x1b[49m▀▀\x1b[38;5;26m\x1b[49m▌\x1b[38;5;75m\x1b[49m  \x1b[38;5;69m\x1b[49m▐\x1b[38;5;75m\x1b[49m▀▀\x1b[38;5;26m\x1b[49m▌\x1b[38;5;69m\x1b[49m▐\x1b[38;5;75m\x1b[49m▀▀\x1b[38;5;26m\x1b[49m▌\x1b[38;5;75m\x1b[49m  \x1b[38;5;69m\x1b[49m▐\x1b[38;5;75m\x1b[49m▀▀\x1b[38;5;26m\x1b[49m▌\x1b[38;5;75m\x1b[49m      \x1b[38;5;69m\x1b[49m▐\x1b[38;5;75m\x1b[49m▀▀\x1b[38;5;26m\x1b[49m▌\x1b[38;5;75m\x1b[49m   \x1b[38;5;69m\x1b[49m▐\x1b[38;5;75m\x1b[49m▀▀\x1b[38;5;26m\x1b[49m▌\x1b[38;5;75m\x1b[49m   \x1b[0m")
	fmt.Println("\x1b[38;5;235m\x1b[49m \x1b[38;5;75m\x1b[49m \x1b[38;5;69m\x1b[49m▐\x1b[38;5;69m\x1b[48;5;26m▄▄▄▄▄▄▄\x1b[38;5;26m\x1b[49m▌\x1b[38;5;75m\x1b[49m \x1b[38;5;69m\x1b[49m▐\x1b[38;5;69m\x1b[48;5;26m▄▄▄▄▄▄\x1b[38;5;26m\x1b[49m▌\x1b[38;5;75m\x1b[49m \x1b[38;5;69m\x1b[49m▐\x1b[38;5;69m\x1b[48;5;26m▄▄▄▄▄▄▄\x1b[38;5;26m\x1b[49m▌\x1b[38;5;75m\x1b[49m    \x1b[38;5;69m\x1b[49m▐\x1b[38;5;69m\x1b[48;5;26m▄▄\x1b[38;5;26m\x1b[49m▌\x1b[38;5;75m\x1b[49m   \x1b[0m")
	fmt.Println("\x1b[38;5;235m\x1b[49m \x1b[0m")
	fmt.Println("\x1b[38;5;235m\x1b[49m \x1b[38;5;69m\x1b[49m  GOYT - A WEB INTERFACE FOR YT-DLP\x1b[0m")
	fmt.Println("\x1b[38;5;235m\x1b[49m                                         \x1b[0m")
}

func print16Banner() {
	fmt.Println("\x1b[30m\x1b[49m                                         \x1b[0m")
	fmt.Println("\x1b[30m\x1b[49m \x1b[37m\x1b[49m ▐\x1b[94;44m▄▄▄▄▄▄▄\x1b[94m\x1b[49m▌\x1b[37m\x1b[49m ▐\x1b[94;44m▄▄▄▄▄▄\x1b[94m\x1b[49m▌\x1b[37m\x1b[49m ▐\x1b[94;44m▄▄\x1b[94m\x1b[49m▌\x1b[37m\x1b[49m  ▐\x1b[94;44m▄▄\x1b[94m\x1b[49m▌\x1b[37m\x1b[49m▐\x1b[94;44m▄▄▄▄▄▄▄▄\x1b[94m\x1b[49m▌\x1b[0m")
	fmt.Println("\x1b[30m\x1b[49m \x1b[37m\x1b[49m▐\x1b[94;44m██\x1b[94m\x1b[49m▌\x1b[37m\x1b[49m      ▐\x1b[94;44m██\x1b[94m\x1b[49m▌\x1b[37m\x1b[49m  ▐\x1b[94;44m██\x1b[94m\x1b[49m▌\x1b[37m\x1b[49m▐\x1b[94;44m██\x1b[94m\x1b[49m▌\x1b[37m\x1b[49m  ▐\x1b[94;44m██\x1b[94m\x1b[49m▌\x1b[37m\x1b[49m   ▐\x1b[94;44m██\x1b[94m\x1b[49m▌\x1b[37m\x1b[49m   \x1b[0m")
	fmt.Println("\x1b[30m\x1b[49m \x1b[37m\x1b[49m▐\x1b[94;44m██\x1b[94m\x1b[49m▌\x1b[37m\x1b[49m▐\x1b[94m\x1b[49m██\x1b[94;44m██\x1b[94m\x1b[49m▌\x1b[37m\x1b[49m▐\x1b[94;44m██\x1b[94m\x1b[49m▌\x1b[37m\x1b[49m  ▐\x1b[94;44m██\x1b[94m\x1b[49m▌\x1b[37m\x1b[49m ▐\x1b[94;44m███████\x1b[94m\x1b[49m▌\x1b[37m\x1b[49m   ▐\x1b[94;44m██\x1b[94m\x1b[49m▌\x1b[37m\x1b[49m   \x1b[0m")
	fmt.Println("\x1b[30m\x1b[49m \x1b[94m\x1b[49m▐\x1b[37m\x1b[49m▀▀\x1b[34m\x1b[49m▌\x1b[37m\x1b[49m  \x1b[94m\x1b[49m▐\x1b[37m\x1b[49m▀▀\x1b[34m\x1b[49m▌\x1b[94m\x1b[49m▐\x1b[37m\x1b[49m▀▀\x1b[34m\x1b[49m▌\x1b[37m\x1b[49m  \x1b[94m\x1b[49m▐\x1b[37m\x1b[49m▀▀\x1b[34m\x1b[49m▌\x1b[37m\x1b[49m      \x1b[94m\x1b[49m▐\x1b[37m\x1b[49m▀▀\x1b[34m\x1b[49m▌\x1b[37m\x1b[49m   \x1b[94m\x1b[49m▐\x1b[37m\x1b[49m▀▀\x1b[34m\x1b[49m▌\x1b[37m\x1b[49m   \x1b[0m")
	fmt.Println("\x1b[30m\x1b[49m \x1b[37m\x1b[49m \x1b[94m\x1b[49m▐\x1b[94;44m▄▄▄▄▄▄▄\x1b[34m\x1b[49m▌\x1b[37m\x1b[49m \x1b[94m\x1b[49m▐\x1b[94;44m▄▄▄▄▄▄\x1b[34m\x1b[49m▌\x1b[37m\x1b[49m \x1b[94m\x1b[49m▐\x1b[94;44m▄▄▄▄▄▄▄\x1b[34m\x1b[49m▌\x1b[37m\x1b[49m    \x1b[94m\x1b[49m▐\x1b[94;44m▄▄\x1b[34m\x1b[49m▌\x1b[37m\x1b[49m   \x1b[0m")
	fmt.Println("\x1b[30m\x1b[49m \x1b[0m")
	fmt.Println("\x1b[30m\x1b[49m \x1b[94m\x1b[49m  GOYT - A WEB INTERFACE FOR YT-DLP\x1b[0m")
	fmt.Println("\x1b[30m\x1b[49m                                         \x1b[0m")
}

func printPlainBanner() {
	fmt.Println("                                         ")
	fmt.Println("  ▐▄▄▄▄▄▄▄▌ ▐▄▄▄▄▄▄▌ ▐▄▄▌  ▐▄▄▌▐▄▄▄▄▄▄▄▄▌")
	fmt.Println(" ▐██▌      ▐██▌  ▐██▌▐██▌  ▐██▌   ▐██▌   ")
	fmt.Println(" ▐██▌▐████▌▐██▌  ▐██▌ ▐███████▌   ▐██▌   ")
	fmt.Println(" ▐▀▀▌  ▐▀▀▌▐▀▀▌  ▐▀▀▌      ▐▀▀▌   ▐▀▀▌   ")
	fmt.Println("  ▐▄▄▄▄▄▄▄▌ ▐▄▄▄▄▄▄▌ ▐▄▄▄▄▄▄▄▌    ▐▄▄▌   ")
	fmt.Println(" ")
	fmt.Println("   GOYT - A WEB INTERFACE FOR YT-DLP")
	fmt.Println("                                         ")
}
