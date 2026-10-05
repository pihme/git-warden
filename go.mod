module github.com/pihme/git-warden

go 1.24

require gopkg.in/yaml.v3 v3.0.1

require pgregory.net/rapid v1.3.0 // test-only (//go:build fuzz)
