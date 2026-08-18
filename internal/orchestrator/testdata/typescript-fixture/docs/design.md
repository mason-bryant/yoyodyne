# Greetings module design

The `src/` directory holds one module per phrase the library can produce. Each
module exports exactly what callers need and nothing else.

Greeting functions are pure functions: they take the name to address and return
the finished string. They never write to a console, read the clock, or hold
state between calls, so a caller can compose them without knowing where the
result is going.

Sources are indented with two spaces. `scripts/` holds the checks that verify
both rules.
