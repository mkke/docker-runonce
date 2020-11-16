module github.com/mkke/docker-runonce

go 1.15

replace github.com/mkke/go-docker => ../go-docker

replace github.com/mkke/go-mlog => ../go-mlog

require (
	docker.io/go-docker v1.0.0
	github.com/Microsoft/go-winio v0.4.15 // indirect
	github.com/docker/distribution v2.7.1+incompatible // indirect
	github.com/docker/docker v1.13.1 // indirect
	github.com/docker/go-connections v0.4.0 // indirect
	github.com/docker/go-units v0.4.0 // indirect
	github.com/dustin/go-humanize v1.0.0
	github.com/gofrs/flock v0.8.0
	github.com/mkke/go-docker v0.0.0-00010101000000-000000000000
	github.com/mkke/go-mlog v0.0.0-20201115114057-047aed649499
	github.com/mkke/go-signalerror v0.0.0-20201114113032-fbc42d633129
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/opencontainers/image-spec v1.0.1 // indirect
	github.com/pkg/errors v0.9.1
	github.com/spf13/cobra v1.1.1
)
