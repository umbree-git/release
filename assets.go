package release

import "embed"

//go:embed tools/bootstrap.template.sh tools/modules/*.sh umbree-release.pub site/index.html site/favicon.svg
var Assets embed.FS
