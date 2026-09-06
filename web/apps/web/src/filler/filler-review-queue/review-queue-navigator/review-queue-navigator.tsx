import { pluralize } from "@loomarr/core/format";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";

interface ReviewQueueItem {
  id: string;
  question: string;
  subject: string;
}

interface ReviewQueueNavigatorProps {
  items: ReviewQueueItem[];
  selectedID: string;
  total: number;
  pageNumber: number;
  pageCount: number;
  hasPreviousPage: boolean;
  hasNextPage: boolean;
  paging: boolean;
  onSelect: (id: string) => void;
  onPreviousPage: () => void;
  onNextPage: () => void;
}

const ReviewQueueNavigator = ({
  items,
  selectedID,
  total,
  pageNumber,
  pageCount,
  hasPreviousPage,
  hasNextPage,
  paging,
  onSelect,
  onPreviousPage,
  onNextPage,
}: ReviewQueueNavigatorProps) => (
  <Card className="h-fit min-w-0 p-3 lg:sticky lg:top-4">
    <div className="flex items-start justify-between gap-3 px-1">
      <div>
        <h2 id="review-queue-navigation-heading" className="font-semibold text-sm">
          Review queue
        </h2>
        <p className="mt-0.5 text-muted-foreground text-xs">
          {pluralize(total, "question")} waiting · Page {pageNumber} of {pageCount}
        </p>
      </div>
    </div>

    <nav aria-labelledby="review-queue-navigation-heading" className="mt-3 overflow-hidden">
      <ol className="flex gap-1 overflow-x-auto pb-2 lg:grid lg:overflow-visible lg:pb-0">
        {items.map((item, index) => {
          const selected = item.id === selectedID;
          return (
            <li key={item.id} className="min-w-60 lg:min-w-0">
              <Button
                type="button"
                variant={selected ? "secondary" : "ghost"}
                aria-current={selected ? "true" : undefined}
                aria-label={`Review question ${index + 1}: ${item.question}`}
                className="h-auto w-full justify-start overflow-hidden px-3 py-2 text-left"
                onClick={() => onSelect(item.id)}
              >
                <span className="w-5 shrink-0 font-mono text-muted-foreground text-xs">{index + 1}</span>
                <span className="min-w-0">
                  <span className="block truncate">{item.question}</span>
                  <span className="mt-0.5 block truncate font-normal text-muted-foreground text-xs">
                    {item.subject}
                  </span>
                </span>
              </Button>
            </li>
          );
        })}
      </ol>
    </nav>

    <div className="mt-3 grid grid-cols-2 gap-2 border-border border-t pt-3">
      <Button
        type="button"
        variant="outline"
        size="sm"
        disabled={!hasPreviousPage || paging}
        onClick={onPreviousPage}
      >
        Previous page
      </Button>
      <Button
        type="button"
        variant="outline"
        size="sm"
        disabled={!hasNextPage || paging}
        onClick={onNextPage}
      >
        Next page
      </Button>
    </div>
  </Card>
);

export type { ReviewQueueItem };
export { ReviewQueueNavigator };
