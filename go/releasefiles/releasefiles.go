// Package releasefiles names the model files the release Setup catalogue
// offers: the recommended coding model and the files of the starter and
// offered picture models, each by the SHA-256 and size its bytes must have.
// The runtime lets operator programs read exactly these files through the
// storage inventory, so Setup can find a copy already on this computer.
//
// The values are a literal projection of the release Setup catalogue, which
// owns them; tests hold the two equal, and hold every file the compiled-in
// catalogue (service/catalog.json) names to one of these. The package imports
// only the standard library.
package releasefiles

import "slices"

// files is each release model file's SHA-256, lowercase hex, and its size in
// bytes. A file two models share appears once.
var files = []struct {
	sha256 string
	bytes  int64
}{
	// The recommended coding model's weights.
	{"55983c5a75a1ab969824077b3bb3de4146e82a9234072b48ad4e8f92ad3fe9f1", 22853663008},
	// The Qwen3-4B text encoder of the starter model and Z-Image-Turbo.
	{"6c671498573ac2f7a5501502ccce8d2b08ea6ca2f661c458e708f36b36edfc5a", 8044982048},
	// The starter picture model, FLUX.2 Klein BF16: main weights and VAE.
	{"ec3d4e733a771f61c052fb4856c48b336c55eaf2c65487c2a1faeb9bbda7a343", 7751105712},
	{"868fe7b343cc8f3a19dbcfcafbc3d5f888802be3f89bd81b65b3621a066ce8f3", 336211292},
	// Z-Image-Turbo: main weights and VAE.
	{"2407613050b809ffdff18a4ac99af83ea6b95443ecebdf80e064a79c825574a6", 12309866400},
	{"afc8e28272cd15db3919bacdb6918ce9c1ed22e96cb12c4d5ed0fba823529e38", 335304388},
	// Qwen-Image-2512: main weights, text encoder and VAE.
	{"c077d5a3f867b6441235d071d76912ce6575db9832651e8b1769e8329d33d798", 14400813600},
	{"8edd10e3ae170ab8b8d114e70763ff63a220f16deec9a56acf9330034efc0bb5", 4785159040},
	{"a70580f0213e67967ee9c95f05bb400e8fb08307e017a924bf3441223e023d1f", 253806246},
}

// ModelDigests is the SHA-256, lowercase hex, of every release model file,
// sorted, once each. Each call returns a new slice.
func ModelDigests() []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.sha256)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// Files is every release model file's size in bytes by its SHA-256,
// lowercase hex. Each call returns a new map.
func Files() map[string]int64 {
	out := make(map[string]int64, len(files))
	for _, f := range files {
		out[f.sha256] = f.bytes
	}
	return out
}
