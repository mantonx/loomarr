import { createFileRoute } from "@tanstack/react-router";
import { FillerPage } from "@/filler/filler-page";

// Incoming — what has arrived but isn't filed yet. Its own path (V-nav-paths), same as
// Catalog and Sources. ⚠ Carries NONE of the catalog's filters: `q`, `kind` and `audience`
// narrow the clip grid, and nothing on this tab is a clip — a search term dragged over from
// Catalog would leave a filter applied to a list it cannot narrow (the same reasoning the
// old `catalogSearch` carve-out in filler-page recorded).
const IncomingScreen = () => <FillerPage tab="incoming" />;

const Route = createFileRoute("/_authed/filler/incoming")({ component: IncomingScreen });

export { Route };
