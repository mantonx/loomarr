package fillersafety

func validProposerIdentity(identity proposerIdentity) bool {
	if !boundedAuthorityID(identity.Implementation) || !validSHA256(identity.ConfigSHA256) {
		return false
	}
	switch identity.Kind {
	case proposerDeterministic:
		return identity.Platform == "" && identity.RuntimeVersion == "" && identity.RuntimeSHA256 == "" && identity.ModelSHA256 == ""
	case proposerExternalModel:
		return boundedAuthorityID(identity.Platform) && boundedAuthorityID(identity.RuntimeVersion) &&
			validSHA256(identity.RuntimeSHA256) && validSHA256(identity.ModelSHA256)
	default:
		return false
	}
}

func validProposalPlan(plan *CompleteMediaPlan) bool {
	return plan != nil && plan.snapshot != nil &&
		validSHA256(plan.AuthoritySHA256) && validSHA256(plan.PolicySHA256) && validSHA256(plan.SourceSHA256) && validToolIdentity(plan.FFmpeg) &&
		plan.SourceBytes > 0 && plan.SourcePath == plan.snapshot.Path() &&
		plan.SourceSHA256 == plan.snapshot.SHA256() && plan.SourceBytes == plan.snapshot.Bytes() &&
		plan.Audio.StartMS == 0 && plan.Audio.EndMS > 0 && plan.Audio == plan.Video
}
