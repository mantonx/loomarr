import { createFileRoute, redirect } from "@tanstack/react-router";
import { FillerPage } from "@/filler/filler-page";
import { type FillerSearch, validateCatalogSearch } from "@/filler/filler-search";

// The clip catalog's filters live in the URL so a filtered/searched view is shareable and
// back-button-aware (the same deep-link contract as the channel-detail section routes).
// Each field is narrowed to a known value / dropped, so a hand-typed or stale link can't
// push garbage into the filler list query.
//
// ⚠ `tab` is GONE from this type (V-nav-paths): which section shows is now the PATH
// (`/filler`, `/filler/incoming`, `/filler/sources`), matching Settings. These four stay
// query params because they are catalog FILTERS, not navigation — a filtered/searched view
// needs to be shareable independent of which section it was reached from.

// Browsing and previewing filler is readable by any authenticated user (it explains why a
// channel plays what it plays); the mutating actions inside are admin-gated, and the
// server enforces that regardless (§11, §19).
const FillerScreen = () => <FillerPage tab="overview" />;

const Route = createFileRoute("/_authed/filler/")({
  // ⚠ The legacy `?tab=` carrier. It is NARROWED here and redirected in `beforeLoad` below —
  // never dropped, or the redirect would have nothing to read.
  validateSearch: (search: Record<string, unknown>): FillerSearch & { tab?: "incoming" | "sources" } => ({
    ...validateCatalogSearch(search),
    ...(search.tab === "incoming" || search.tab === "sources" ? { tab: search.tab } : {}),
  }),
  // ⚠ `?tab=incoming` / `?tab=sources` are old bookmarks and shared links (pre-nav-paths). The
  // section is a path segment now, so they redirect to it, carrying every other search param
  // through unchanged.
  //
  // ⚠ **In `beforeLoad`, NOT in `validateSearch`.** Throwing a `redirect` from the validator
  // does not navigate — the router is still parsing the URL at that point and treats the throw
  // as a parse failure, so the page rendered BLANK at the old address. That is the exact
  // bookmark-compatibility promise this code exists to keep, and every unit test passed while
  // it was broken; only loading the URL in a browser showed it.
  beforeLoad: ({ search }) => {
    if (search.tab) {
      const { tab, ...rest } = search;
      throw redirect({ to: `/filler/${tab}`, search: rest });
    }
    if (Object.keys(search).length > 0) {
      throw redirect({ to: "/filler/library", search });
    }
  },
  component: FillerScreen,
});

export { Route };
