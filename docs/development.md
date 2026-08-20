# Development

## Go hot reload

The standard local Docker commands use Air hot reload for `gate`, `protocol`,
`sync`, `ws`, `fignode`, and `graphql`:

```sh
make up
```

Run `make build` once, then `make up`. The selected service Dockerfile builds
its `development` stage, which contains Air. Saving Go source under `go/` or
`tap/` recompiles and restarts the affected service inside its existing
container. Go build and module caches are stored in named Docker volumes, so
normal edits do not rebuild an image.

With `PRODUCTION_SERVER := 1`, this configuration lives directly in
`container/docker-compose.prod.yml`; no Compose override is used. Therefore
`make build`, `make up`, and `make upd` use Air for these services.

Use `make build_prod` when you explicitly need immutable production images; it
selects the `production` target in each Go service Dockerfile.
