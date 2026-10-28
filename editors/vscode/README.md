# tenon for VS Code

The tenon coding agent in VS Code. The extension is a thin ACP client: tools,
permissions, sandboxing, masking, routing and audit all stay in `tenon acp`.
The extension makes no network call and sends no telemetry.

## Requirements

None: each platform package (macOS and Linux, arm64 and x64) bundles the tenon
binary. To use another build, set `tenon.path` to its absolute path. tenon
0.9.0 or later is required.

## Documentation

<https://tenon-code-eu.github.io/tenon/editors/>

## License

EUPL-1.2
