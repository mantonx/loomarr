package filler_test

import (
	"context"
	"testing"

	"github.com/loomarr/loomarr/internal/filler"
)

func TestPipeline_BackfillsLegacyChildrenThroughFailClosedScreening(t *testing.T) {
	st := newPipeMemStore()
	st.put(filler.StoreClip{Clip: filler.Clip{
		Hash: "child", Path: "children/child.mp4", ParentHash: "parent", AutoFiled: true,
	}})
	st.rows["child"] = filler.ClipPipeline{
		ClipHash: "child", Stage: filler.StageScore, Status: filler.StatusDone,
		Disposition: filler.DispositionFiled,
		Stages: []filler.StageRecord{
			{Stage: filler.StageProbe, Status: filler.StatusDone},
			{Stage: filler.StageLanguage, Status: filler.StatusDone},
			{Stage: filler.StageScore, Status: filler.StatusDone},
		},
	}
	pipeline := newPipe(st, []filler.Stage{filler.NewSegmentScreeningStage(nil, nil, "/clips")}, filler.Budget{})

	result, err := pipeline.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	row := st.rows["child"]
	clip := st.clips["child"]
	if result.Requeued != 1 || row.Stage != filler.StageScreen || row.Disposition != filler.DispositionReview ||
		!clip.Held || clip.AutoFiled {
		t.Fatalf("legacy child was not safely rewound: result=%+v row=%+v clip=%+v", result, row, clip)
	}
	if len(row.Stages) != 2 || row.Stages[0].Stage != filler.StageProbe || row.Stages[1].Stage != filler.StageScreen {
		t.Fatalf("legacy downstream records survived screening rewind: %+v", row.Stages)
	}
}

func TestPipeline_ScreeningBackfillLeavesAlreadyCompletedOrNonAirableRowsAlone(t *testing.T) {
	for _, test := range []struct {
		name string
		clip filler.Clip
		row  filler.ClipPipeline
	}{
		{
			name: "completed child", clip: filler.Clip{Hash: "child", Path: "child.mp4", ParentHash: "parent"},
			row: filler.ClipPipeline{ClipHash: "child", Stage: filler.StageScore, Status: filler.StatusDone, Disposition: filler.DispositionFiled,
				Stages: []filler.StageRecord{{Stage: filler.StageScreen, Status: filler.StatusDone}}},
		},
		{
			name: "top level", clip: filler.Clip{Hash: "child", Path: "child.mp4"},
			row: filler.ClipPipeline{ClipHash: "child", Stage: filler.StageScore, Status: filler.StatusDone, Disposition: filler.DispositionFiled},
		},
		{
			name: "hard rejected child", clip: filler.Clip{Hash: "child", Path: "child.mp4", ParentHash: "parent", Held: true},
			row: filler.ClipPipeline{ClipHash: "child", Stage: filler.StageScore, Status: filler.StatusDone,
				Disposition: filler.DispositionRejected, RejectReason: filler.ReasonUnplayable},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			st := newPipeMemStore()
			st.put(filler.StoreClip{Clip: test.clip})
			st.rows["child"] = test.row
			result, err := newPipe(st, nil, filler.Budget{}).RunOnce(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if result.Requeued != 0 || st.rows["child"].Disposition != test.row.Disposition {
				t.Fatalf("unrelated row was requeued: result=%+v row=%+v", result, st.rows["child"])
			}
		})
	}
}
