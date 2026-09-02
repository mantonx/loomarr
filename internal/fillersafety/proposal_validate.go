package fillersafety

func validProposerIdentity(identity proposerIdentity) bool {
	return boundedAuthorityID(identity.Implementation) &&
		boundedAuthorityID(identity.Platform) &&
		boundedAuthorityID(identity.RuntimeVersion) &&
		validSHA256(identity.RuntimeSHA256) &&
		validSHA256(identity.ModelSHA256) &&
		validSHA256(identity.ConfigSHA256)
}

func validProposalPlan(plan *CompleteMediaPlan) bool {
	return plan != nil && plan.snapshot != nil &&
		validSHA256(plan.AuthoritySHA256) && validSHA256(plan.SourceSHA256) &&
		plan.SourceBytes > 0 && plan.SourcePath == plan.snapshot.Path() &&
		plan.SourceSHA256 == plan.snapshot.SHA256() && plan.SourceBytes == plan.snapshot.Bytes() &&
		plan.Audio.StartMS == 0 && plan.Audio.EndMS > 0 && plan.Audio == plan.Video
}
