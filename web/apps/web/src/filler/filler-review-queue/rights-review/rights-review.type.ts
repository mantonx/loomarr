import type { FillerRightsReviewDTO } from "@loomarr/api/models/fillerRightsReviewDTO";

interface RightsReviewProps {
  clipHash: string;
  review: FillerRightsReviewDTO;
  screeningAssessedAt?: string;
}

export type { RightsReviewProps };
