package promoter

import (
	"fmt"
	"strings"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	reg "sigs.k8s.io/promo-tools/v3/legacy/dockerregistry"
)

// Run a snapshot
func (di *defaultPromoterImplementation) Snapshot(opts *Options, rii reg.RegInvImage) error {
	// Run the snapshot
	var snapshot string
	switch strings.ToLower(opts.OutputFormat) {
	case "csv":
		snapshot = rii.ToCSV()
	case "yaml":
		snapshot = rii.ToYAML(reg.YamlMarshalingOpts{})
	default:
		// In the previous cli/run it took any malformed format string. Now we err.
		return errors.Errorf("invalid snapshot output format: %s", opts.OutputFormat)
	}

	// TODO: Maybe store the snapshot somewhere?
	fmt.Println(snapshot)
	return nil
}

func (di *defaultPromoterImplementation) GetSnapshotSourceRegistry(
	opts *Options,
) (*reg.RegistryContext, error) {
	// Build the source registry:
	srcRegistry := &reg.RegistryContext{
		ServiceAccount: opts.SnapshotSvcAcct,
		Src:            true,
	}

	// The only difference when running from Snapshot or
	// ManifestBasedSnapshotOf will be the Name property
	// of the source registry
	if opts.Snapshot != "" {
		srcRegistry.Name = reg.RegistryName(opts.Snapshot)
	} else if opts.ManifestBasedSnapshotOf == "" {
		srcRegistry.Name = reg.RegistryName(opts.ManifestBasedSnapshotOf)
	} else {
		return nil, errors.New(
			"when snapshotting, Snapshot or ManifestBasedSnapshotOf have to be set",
		)
	}

	return srcRegistry, nil
}

// GetSnapshotManifest creates the manifest list from the
// specified snapshot source
func (di *defaultPromoterImplementation) GetSnapshotManifests(
	opts *Options,
) ([]reg.Manifest, error) {
	// Build the source registry:
	srcRegistry, err := di.GetSnapshotSourceRegistry(opts)
	if err != nil {
		return nil, errors.Wrap(err, "building source registry for snapshot")
	}

	// Add it to a new manifest and return it:
	return []reg.Manifest{
		{
			Registries: []reg.RegistryContext{
				*srcRegistry,
			},
			Images: []reg.Image{},
		},
	}, nil
}

// AppendManifestToSnapshot checks if a manifest was specified in the
// options passed to the promoter. If one is found, we parse it and
// append it to the list of manifests generated for the snapshot
// during GetSnapshotManifests()
func (di *defaultPromoterImplementation) AppendManifestToSnapshot(
	opts *Options, mfests []reg.Manifest,
) ([]reg.Manifest, error) {
	// If no manifest was passed in the options, we return the
	// same list of manifests unchanged
	if opts.Manifest == "" {
		logrus.Info("No manifest defined, not appending to snapshot")
		return mfests, nil
	}

	// Parse the specified manifest and append it to the list
	mfest, err := reg.ParseManifestFromFile(opts.Manifest)
	if err != nil {
		return nil, errors.Wrap(err, "parsing specified manifest")
	}

	return append(mfests, mfest), nil
}

//
func (di *defaultPromoterImplementation) GetRegistryImageInventory(
	opts *Options, mfests []reg.Manifest,
) (reg.RegInvImage, error) {
	// I'm pretty sure the registry context here can be the same for
	// both snapshot sources and when running in the original cli/run,
	// In the 2nd case (Snapshot), it was recreated like we do here.
	sc, err := di.MakeSyncContext(opts, mfests)
	if err != nil {
		return nil, errors.Wrap(err, "making sync context for registry inventory")
	}

	srcRegistry, err := di.GetSnapshotSourceRegistry(opts)
	if err != nil {
		return nil, errors.Wrap(err, "creting source registry for image inventory")
	}

	if len(opts.ManifestBasedSnapshotOf) > 0 {
		promotionEdges, err := reg.ToPromotionEdges(mfests)
		if err != nil {
			return nil, errors.Wrap(
				err, "converting list of manifests to edges for promotion",
			)
		}

		// Create the registry inventory
		rii := reg.EdgesToRegInvImage(
			promotionEdges,
			opts.ManifestBasedSnapshotOf,
		)

		if opts.MinimalSnapshot {
			sc.ReadRegistries(
				[]reg.RegistryContext{*srcRegistry},
				true,
				reg.MkReadRepositoryCmdReal,
			)

			sc.ReadGCRManifestLists(reg.MkReadManifestListCmdReal)
			rii = sc.RemoveChildDigestEntries(rii)
		}

		return rii, nil
	}

	sc.ReadRegistries(
		[]reg.RegistryContext{*srcRegistry},
		// Read all registries recursively, because we want to produce a
		// complete snapshot.
		true,
		reg.MkReadRepositoryCmdReal,
	)

	rii := sc.Inv[mfests[0].Registries[0].Name]
	if opts.SnapshotTag != "" {
		rii = reg.FilterByTag(rii, opts.SnapshotTag)
	}

	if opts.MinimalSnapshot {
		logrus.Info("removing tagless child digests of manifest lists")
		sc.ReadGCRManifestLists(reg.MkReadManifestListCmdReal)
		rii = sc.RemoveChildDigestEntries(rii)
	}
	return rii, nil
}
