package fillerstructure

type DirectVideoResponse struct {
	Segments []DirectVideoResponseSegment `json:"segments"`
}

type DirectVideoResponseSegment struct {
	EndMS        int64   `json:"endMs"`
	Role         string  `json:"role"`
	DecisiveAtMS []int64 `json:"decisiveAtMs"`
	Reason       string  `json:"reason"`
}

type DirectVideoClaim struct {
	Kind         string
	DecisiveAtMS []int64
	Reason       string
}

type DirectVideoAssessmentSegment struct {
	StartMS      int64
	EndMS        int64
	Role         Role
	DecisiveAtMS []int64
	Reason       string
}

type DirectVideoAssessment struct {
	Unit     DirectVideoClaim
	Role     *DirectVideoClaim
	Segments []DirectVideoAssessmentSegment
}
