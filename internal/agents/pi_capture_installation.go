package agents

import (
	"embed"
	"errors"
	"os"
	"path/filepath"
)

//go:embed pi-official-capture.ts pi-official-capture-host.ts pi-official-capture-broker.ts
var piCaptureModules embed.FS

var piCaptureModuleNames = []string{"pi-official-capture.ts", "pi-official-capture-host.ts", "pi-official-capture-broker.ts"}

func validatePiCaptureManifest(i Installation) error {
	if i.PiCaptureVersion == 0 && len(i.PiCaptureFiles) == 0 {
		return nil
	}
	if i.Connection.Agent != "pi" || i.PiCaptureVersion != 1 || len(i.PiCaptureFiles) != len(piCaptureModuleNames) || i.ExtensionPath != filepath.Join(i.Options.ClientRoot, "agent-adapters", i.Connection.ID, "memlane.ts") {
		return errors.New("pi capture installation manifest changed; files preserved")
	}
	for _, name := range piCaptureModuleNames {
		if len(i.PiCaptureFiles[name]) != 64 {
			return errors.New("pi capture module receipt incomplete")
		}
	}
	return nil
}

func checkPiCaptureModules(i Installation) error {
	if err := validatePiCaptureManifest(i); err != nil {
		return err
	}
	for _, name := range piCaptureModuleNames {
		expected, present := i.PiCaptureFiles[name]
		if !present {
			continue
		}
		path := filepath.Join(filepath.Dir(i.ExtensionPath), name)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("pi capture module changed externally; files preserved")
		}
		source, err := os.ReadFile(path)
		if err != nil || hash(source) != expected {
			return errors.New("pi capture module changed externally; files preserved")
		}
	}
	return nil
}

// Called under the existing installation transaction and owner mutex. The
// helper adds no settings entry, credential, transcript or capture permission.
func installPiCaptureModules(i *Installation, prior Installation, hasPrior bool, write func(string, []byte, os.FileMode) error) error {
	if hasPrior {
		if err := checkPiCaptureModules(prior); err != nil {
			return err
		}
	}
	for _, name := range piCaptureModuleNames {
		path := filepath.Join(filepath.Dir(i.ExtensionPath), name)
		if !hasPrior || prior.PiCaptureVersion == 0 {
			if _, err := os.Lstat(path); err == nil {
				return errors.New("unowned pi capture module preserved")
			} else if !os.IsNotExist(err) {
				return err
			}
		}
	}
	i.PiCaptureVersion = 1
	i.PiCaptureFiles = map[string]string{}
	// Only this locally verified host API is currently admitted. Other pi
	// versions retain the existing tools but do not start a capture observer.
	i.PiCaptureSupported = i.AgentVersion == "0.84.1"
	for _, name := range piCaptureModuleNames {
		source, err := piCaptureModules.ReadFile(name)
		if err != nil {
			return err
		}
		path := filepath.Join(filepath.Dir(i.ExtensionPath), name)
		if hasPrior && prior.PiCaptureVersion == 1 {
			current, e := os.ReadFile(path)
			if e != nil || hash(current) != prior.PiCaptureFiles[name] {
				return errors.New("pi capture module changed while updating; files preserved")
			}
		} else if _, e := os.Lstat(path); e == nil {
			return errors.New("pi capture module appeared while installing; preserved")
		} else if !os.IsNotExist(e) {
			return e
		}
		if err = write(path, source, 0600); err != nil {
			return err
		}
		i.PiCaptureFiles[name] = hash(source)
	}
	return nil
}
