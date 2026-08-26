package backuprpo

import (
	"os"
	"strconv"
)

const pinnedRuntimeDescriptor = 3

type PinnedBundleInputs struct {
	RuntimeDir   *os.File
	Script       *os.File
	VerifyScript *os.File
	Keys         *os.File
	GPGHome      *os.File
	GPG          *os.File
	Python       *os.File
}

type PinnedManifestInputs struct {
	RuntimeDir   *os.File
	VerifyScript *os.File
	GPG          *os.File
	Python       *os.File
	GPGHome      *os.File
}

func NewPinnedShellBundleCreator(
	config ShellBundleCreatorConfig,
	source BackupSource,
	inputs PinnedBundleInputs,
) (*ShellBundleCreator, error) {
	if inputs.RuntimeDir == nil || inputs.Script == nil || inputs.VerifyScript == nil ||
		inputs.Keys == nil || inputs.GPGHome == nil || inputs.GPG == nil || inputs.Python == nil {
		return nil, ErrInvalidConfig
	}
	config.RuntimeDir = procDescriptorPath(pinnedRuntimeDescriptor)
	config.ScriptPath = procDescriptorPath(4)
	config.VerifyScriptPath = procDescriptorPath(5)
	config.KeysPath = procDescriptorPath(6)
	config.GPGHomeFD = procDescriptorPath(7)
	config.GPGPath = procDescriptorPath(8)
	config.PythonPath = procDescriptorPath(9)
	runtime, err := newPinnedBundleRuntime(inputs.RuntimeDir, config.RuntimeDir)
	if err != nil {
		return nil, ErrInvalidConfig
	}
	creator, err := newShellBundleCreator(config, source, runtime, osCommandRunner{})
	if err != nil {
		return nil, err
	}
	creator.gpgHomeFile = inputs.GPGHome
	creator.commandFiles = []*os.File{
		inputs.RuntimeDir,
		inputs.Script,
		inputs.VerifyScript,
		inputs.Keys,
		inputs.GPGHome,
		inputs.GPG,
		inputs.Python,
	}
	return creator, nil
}

func NewPinnedShellManifestVerifier(
	config ManifestVerifierConfig,
	inputs PinnedManifestInputs,
) (*ShellManifestVerifier, error) {
	if inputs.RuntimeDir == nil || inputs.VerifyScript == nil || inputs.GPG == nil ||
		inputs.Python == nil || inputs.GPGHome == nil {
		return nil, ErrInvalidConfig
	}
	config.RuntimeDir = procDescriptorPath(pinnedRuntimeDescriptor)
	config.VerifyScriptPath = procDescriptorPath(4)
	config.GPGPath = procDescriptorPath(5)
	config.PythonPath = procDescriptorPath(6)
	runtime, err := newPinnedManifestVerificationRuntime(inputs.RuntimeDir, config.RuntimeDir)
	if err != nil {
		return nil, ErrInvalidConfig
	}
	verifier, err := newShellManifestVerifier(config, runtime, osCommandRunner{})
	if err != nil {
		return nil, err
	}
	verifier.gpgHomeFile = inputs.GPGHome
	verifier.decryptLeadFile = inputs.RuntimeDir
	verifier.commandFiles = []*os.File{
		inputs.VerifyScript,
		inputs.GPG,
		inputs.Python,
		inputs.GPGHome,
	}
	return verifier, nil
}

func pinnedDirectoryMatchesPath(path string, pinned *os.File) bool {
	if pinned == nil {
		return true
	}
	pathInfo, err := os.Lstat(path)
	if err != nil || pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.IsDir() {
		return false
	}
	pinnedInfo, err := pinned.Stat()
	if err != nil || !pinnedInfo.IsDir() {
		return false
	}
	return os.SameFile(pathInfo, pinnedInfo)
}

func procDescriptorPath(descriptor int) string {
	return "/proc/self/fd/" + strconv.Itoa(descriptor)
}
