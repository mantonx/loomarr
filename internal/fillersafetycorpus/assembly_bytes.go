package fillersafetycorpus

import "fmt"

type assemblyByteTracker struct {
	maximumInput, maximumOutput int64
	input, output               int64
	inputs                      map[string]FileAuthority
}

type cohortByteTracker struct {
	maximum int64
	bytes   int64
	inputs  map[string]FileAuthority
}

func (tracker *assemblyByteTracker) addUniqueInput(root string, authority FileAuthority) error {
	key := root + "\x00" + authority.Path
	if previous, found := tracker.inputs[key]; found {
		if previous != authority {
			return fmt.Errorf("spoken corpus input path has conflicting authorities")
		}
		return nil
	}
	if authority.Bytes > tracker.maximumInput-tracker.input {
		return fmt.Errorf("spoken corpus assembly exceeds its input byte ceiling")
	}
	tracker.inputs[key], tracker.input = authority, tracker.input+authority.Bytes
	return nil
}

func (tracker *assemblyByteTracker) addDocument(root, path, sha string, bytes int64) error {
	return tracker.addUniqueInput(root, FileAuthority{Path: path, SHA256: sha, Bytes: bytes})
}

func (tracker *assemblyByteTracker) addOutput(bytes int64) error {
	if bytes <= 0 || bytes > tracker.maximumOutput-tracker.output {
		return fmt.Errorf("spoken corpus assembly exceeds its output byte ceiling")
	}
	tracker.output += bytes
	return nil
}

func (tracker *assemblyByteTracker) snapshot(root string, authority FileAuthority, output string, cohort *cohortByteTracker) error {
	key := root + "\x00" + authority.Path
	if previous, found := cohort.inputs[key]; found {
		if previous != authority {
			return fmt.Errorf("spoken corpus cohort path has conflicting authorities")
		}
	} else {
		if authority.Bytes > cohort.maximum-cohort.bytes {
			return fmt.Errorf("spoken corpus cohort exceeds its byte ceiling")
		}
		cohort.inputs[key], cohort.bytes = authority, cohort.bytes+authority.Bytes
	}
	if err := tracker.addUniqueInput(root, authority); err != nil {
		return err
	}
	if err := tracker.addOutput(authority.Bytes); err != nil {
		return err
	}
	return snapshotPrivateAssemblyFile(root, authority, output, cohort.maximum)
}
