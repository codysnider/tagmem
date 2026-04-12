set shell := ["bash", "-cu"]

build:
	./scripts/cmd/docker-build/run.sh

release:
	./scripts/cmd/release/run.sh

release-patch:
	./scripts/cmd/release/run.sh patch

release-minor:
	./scripts/cmd/release/run.sh minor

release-major:
	./scripts/cmd/release/run.sh major

release-image:
	./scripts/cmd/release/run.sh

shell:
	./scripts/cmd/docker-shell/run.sh

doctor:
	./scripts/cmd/docker-doctor/run.sh

datasets:
	./scripts/cmd/docker-datasets/run.sh

bench-longmemeval:
	./scripts/cmd/docker-bench-longmemeval/run.sh

bench-suite:
	./scripts/cmd/docker-bench-suite/run.sh

e2e-smoke:
	./scripts/cmd/docker-e2e-smoke/run.sh

release-check:
	./scripts/cmd/release-check/run.sh
