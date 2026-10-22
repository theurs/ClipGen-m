# Lua Executor Utility

This is a simple utility that allows executing Lua scripts as expressions, primarily designed for mathematical calculations and algorithm execution in the Mistral CLI tool.

## Usage

```bash
go run main.go "2 + 3 * 4"
```

Or build and run:

```bash
go build .
./lua-executor "math.sqrt(16)"
```

## Examples

- Basic math: `go run main.go "2 + 3 * 4"` → `14`
- Math functions: `go run main.go "math.sqrt(16)"` → `4`
- Variables: `go run main.go "x = 5; return x * 2"` → `10`
- Trigonometry: `go run main.go "math.sin(math.pi / 2)"` → `1`
- String formatting: `go run main.go "string.format('%.2f', 3.14159)"` → `3.14`
- Algorithms: `go run main.go "function factorial(n) if n <= 1 then return 1 else return n * factorial(n-1) end; return factorial(10)"` → `3628800`

## Integration

This utility is used as an integral part of the Mistral CLI tool as a calculator for precise mathematical computations and algorithm execution. It enables Mistral to perform complex calculations and execute Lua algorithms safely.

## Features

- Mathematical expressions evaluation
- Support for all Lua math functions (math, string, table operations, etc.)
- Recursive function execution
- Loop processing
- Safe execution environment