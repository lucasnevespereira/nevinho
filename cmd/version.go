package cmd

import "fmt"

// Version prints the binary's version string. Backs `nevinho version`,
// `nevinho --version`, and `nevinho -v`.
func Version(version string) {
	fmt.Println("nevinho " + version)
}
