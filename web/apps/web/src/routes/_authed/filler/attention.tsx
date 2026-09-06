import { createFileRoute, redirect } from "@tanstack/react-router";

const Route = createFileRoute("/_authed/filler/attention")({
  beforeLoad: () => {
    throw redirect({ to: "/filler/incoming" });
  },
});

export { Route };
