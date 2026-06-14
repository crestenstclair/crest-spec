package main

type cliFlags struct {
	addr string
}

func parseFlags(args []string) cliFlags {
	var f cliFlags
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--addr":
			if i+1 < len(args) {
				f.addr = args[i+1]
				i++
			}
		}
	}
	return f
}
