package bootstrap

import "github.com/zwhu/memlane/core/setup"

// ManualInstallCommand returns copyable commands for the selected dependency.
func ManualInstallCommand(item Item, env *Environment, downloadSource string) string {
	return setup.ManualInstallCommand(item.toInstallerItem(), manualCommandContext(env), downloadSource)
}

// ManualInstallScript joins copyable terminal commands for all missing/incompatible items.
func ManualInstallScript(plan Plan, env *Environment, downloadSource string) string {
	return setup.ManualInstallScript(plan.toInstallerPlan(), manualCommandContext(env), downloadSource)
}

func manualCommandContext(env *Environment) setup.ManualCommandContext {
	if env == nil {
		return setup.ManualCommandContext{}
	}
	model := setupModelSpec(env.Model.Spec)
	ctx := setup.ManualCommandContext{PluginSpec: &env.Plugin.Spec, ModelSpec: &model}
	if n := env.Runtime.Native; n != nil {
		ctx.NativeSpec = &setup.NativeSupportSpec{
			Version:       n.Version,
			Artifact:      setup.Artifact(n.Artifact),
			ArchivePrefix: n.ArchivePrefix,
		}
	}
	return ctx
}

func setupModelSpec(source ModelSpec) setup.ModelSpec {
	spec := setup.ModelSpec{Schema: source.Schema, Model: source.Model, Provider: source.Provider, Dimensions: source.Dimensions}
	for _, repo := range source.Repositories {
		target := setup.ModelRepository{Repo: repo.Repo, Revision: repo.Revision}
		for _, file := range repo.Files {
			target.Files = append(target.Files, setup.ModelFile(file))
		}
		spec.Repositories = append(spec.Repositories, target)
	}
	return spec
}
