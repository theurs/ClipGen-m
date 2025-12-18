package main

import (
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/yuin/gopher-lua"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: lua-executor \"expression\"")
		fmt.Println("Example: lua-executor \"2 + 3 * 4\"")
		fmt.Println("For assignments, use: lua-executor \"x = 5; return x * 2\"")
		return
	}

	// Get the Lua code from command line arguments
	luaCode := strings.Join(os.Args[1:], " ")

	// Create a new Lua state
	L := lua.NewState()
	defer L.Close()

	// Open standard libraries
	L.OpenLibs()

	// First, try to execute as an expression by wrapping in return
	exprScript := fmt.Sprintf("return (%s)", luaCode)

	err := L.DoString(exprScript)
	if err != nil {
		// If expression failed, try as a statement/script
		err2 := L.DoString(luaCode)
		if err2 != nil {
			log.Fatal("Error executing Lua code as expression: ", err, " or as statement: ", err2)
		}
	}

	// Get the result from the stack (if any)
	top := L.GetTop()
	if top > 0 {
		result := L.ToString(-1)
		if result != "" {
			fmt.Printf("%s\n", result)
		}
	}
}