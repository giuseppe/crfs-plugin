module github.com/giuseppe/crfs-plugin/go

go 1.13

require (
	github.com/containers/image v3.0.2+incompatible
	github.com/containers/image/v4 v4.0.1
	github.com/docker/go-metrics v0.0.1 // indirect
	github.com/google/crfs v0.0.0-20191023181012-53a7ed851890
	github.com/modern-go/reflect2 v1.0.1 // indirect
	github.com/pkg/errors v0.8.2-0.20190227000051-27936f6d90f9
)

replace github.com/containers/image/v4 => github.com/giuseppe/image/v4 v4.0.0-20191024080111-a8771ca8ef72
