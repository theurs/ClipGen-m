# Lua Executor Utility

This is a simple utility that allows executing Lua scripts as expressions, primarily designed for testing Lua execution capabilities.

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