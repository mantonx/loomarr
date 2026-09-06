// Which section is showing. Each primary destination has its own path,
// so the page that owns the route tells FillerPage which one it is rather than the
// component reading `?tab=` itself — three different route files render this component.
type FillerTab = "overview" | "incoming" | "library" | "manage" | "sources" | "taxonomy";

type FillerPageProps = {
  tab: FillerTab;
};

export type { FillerPageProps, FillerTab };
