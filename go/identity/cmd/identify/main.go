package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/openabstractions/abstraction-model/go/identity"
)

func main() {
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 1<<20), 1<<20)
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()
	for in.Scan() {
		line := strings.TrimRight(in.Text(), "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		n := identity.Parse(strings.SplitN(line, "\t", 2)[0])
		fmt.Fprintf(out, "%s\t%s\n", n.Family, n.Quant)
	}
}
