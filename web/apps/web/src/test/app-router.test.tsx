import type { MeBody } from "@loomarr/api";
import {
  getLoginMockHandler,
  getPreviewInvitationMockHandler,
  getPreviewPasswordRecoveryMockHandler,
  getRedeemPasswordRecoveryMockHandler,
  getRequestPasswordRecoveryMockHandler,
} from "@loomarr/api/msw";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRouter, RouterProvider } from "@tanstack/react-router";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { clearAccountActionGrant } from "@/auth/account-action-grant";
import { routeTree } from "@/routeTree.gen";
import { appHandlers } from "@/test/msw/handlers";
import { server } from "@/test/msw/server";

// Router-level auth coverage (§11, §19): the _authed beforeLoad guard + the login flow,
// driven through the REAL generated route tree. `me` flips to 200 once a login POST
// lands, so the sign-in test exercises the guard re-running on navigation.
//
// ⚠ `local` is REQUIRED on MeBody and the old fixture omitted it — the fourth file in this
// migration carrying that same incomplete user.
const ADMIN: MeBody = {
  id: "u1",
  name: "Ada",
  role: "admin",
  local: true,
  offlineLogin: false,
  autoApprove: true,
  disabled: false,
  quota: 0,
};

// ⚠ THE POINT OF `appHandlers`: this test mounts the REAL route tree, so the app fetches whatever
// each landed route needs. The stub this replaced answered ALL of that with a catch-all
// `json({}, 200)` — every screen in the suite rendered against empty objects and nothing said so.
// `appHandlers` is the shared baseline (see src/test/msw/handlers.ts); this file adds only the
// auth behaviour it is actually testing.
//
// `me` is hand-written because it is STATEFUL (401 → 200 after login) and status-bearing, which
// generated handlers cannot express — the spec declares errors via `default:` with no 401 code.
const stubAuth = (startAuthed: boolean) => {
  let authed = startAuthed;
  const logins: unknown[] = [];
  server.use(
    ...appHandlers(),
    http.get("*/v1/auth/me", () =>
      authed ? HttpResponse.json(ADMIN) : HttpResponse.json({ title: "Unauthorized" }, { status: 401 }),
    ),
    getLoginMockHandler(async ({ request }) => {
      logins.push(await request.json());
      authed = true;
      return ADMIN;
    }),
  );
  return { logins };
};

const renderApp = (initialPath: string) => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const router = createRouter({
    routeTree,
    context: { queryClient },
    history: createMemoryHistory({ initialEntries: [initialPath] }),
  });
  render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  // Returned so a test can assert WHERE the router settled — the only way to prove a redirect
  // actually navigated rather than leaving the old URL rendering nothing.
  return router;
};

describe("invitation join route", () => {
  it("removes the fragment before rendering credentials and previews without redeeming", async () => {
    clearAccountActionGrant();
    const bearer = "e".repeat(64);
    const previewBodies: unknown[] = [];
    window.history.replaceState({}, "", `/join#grant=${bearer}`);
    server.use(
      getPreviewInvitationMockHandler(async ({ request }) => {
        previewBodies.push(await request.json());
        return {
          kind: "local",
          username: "Ada",
          role: "member",
          credentialPath: "local_password",
          expiresAt: Date.now() + 60_000,
        };
      }),
    );

    renderApp("/join");

    expect(await screen.findByLabelText("Create password")).toBeInTheDocument();
    expect(window.location.hash).toBe("");
    expect(previewBodies).toEqual([{ grant: bearer }]);
    expect(screen.getByRole("button", { name: "Activate account" })).toBeInTheDocument();
  });
});

describe("password recovery routes", () => {
  it("submits a generic request without revealing eligibility", async () => {
    const bodies: unknown[] = [];
    server.use(
      getRequestPasswordRecoveryMockHandler(async ({ request }) => {
        bodies.push(await request.json());
        return { message: "If that account can be recovered here, Loomarr will send an email." };
      }),
    );
    renderApp("/forgot-password");
    await userEvent.type(await screen.findByLabelText("Username"), "Ada");
    await userEvent.click(screen.getByRole("button", { name: /email reset link/i }));
    expect(await screen.findByRole("status")).toHaveTextContent(/if that account can be recovered here/i);
    expect(bodies).toEqual([{ username: "Ada" }]);
  });

  it("cleans the reset bearer before credentials render and resets only on explicit submit", async () => {
    clearAccountActionGrant();
    const bearer = "f".repeat(64);
    const previews: unknown[] = [];
    const redemptions: unknown[] = [];
    window.history.replaceState({}, "", `/reset-password#grant=${bearer}`);
    server.use(
      getPreviewPasswordRecoveryMockHandler(async ({ request }) => {
        previews.push(await request.json());
        return { expiresAt: Date.now() + 60_000 };
      }),
      getRedeemPasswordRecoveryMockHandler(async ({ request }) => {
        redemptions.push(await request.json());
      }),
    );

    renderApp("/reset-password");
    expect(await screen.findByLabelText("New password")).toBeInTheDocument();
    expect(window.location.hash).toBe("");
    expect(localStorage.length).toBe(0);
    expect(sessionStorage.length).toBe(0);
    expect(previews).toEqual([{ grant: bearer }]);
    expect(redemptions).toEqual([]);

    await userEvent.type(screen.getByLabelText("New password"), "replacement-password");
    await userEvent.type(screen.getByLabelText("Confirm new password"), "replacement-password");
    await userEvent.click(screen.getByRole("button", { name: "Reset password" }));
    expect(await screen.findByRole("status")).toHaveTextContent(
      /every existing session has been signed out/i,
    );
    expect(redemptions).toEqual([{ grant: bearer, password: "replacement-password" }]);
  });
});

describe("app router auth", () => {
  it("bounces a signed-out visitor from a protected route to the login form", async () => {
    stubAuth(false);
    renderApp("/guide");
    expect(await screen.findByLabelText("Username")).toBeInTheDocument();
  });

  it("renders the protected screen when already authenticated", async () => {
    stubAuth(true);
    renderApp("/guide");
    expect(await screen.findByRole("heading", { name: "Channels" })).toBeInTheDocument();
    // The shell owns one version query for every route; this proves the generated response reaches
    // the persistent frame, not only the isolated AppShell story.
    expect(await screen.findByRole("link", { name: "Loomarr dev — About" })).toHaveAttribute(
      "href",
      "/settings/system/about",
    );
  });

  it("signs in and lands on the Channels home", async () => {
    const { logins } = stubAuth(false);
    renderApp("/login");

    await userEvent.type(await screen.findByLabelText("Username"), "ada");
    await userEvent.type(screen.getByLabelText("Password"), "hunter2!");
    await userEvent.click(screen.getByRole("button", { name: /sign in/i }));

    expect(await screen.findByRole("heading", { name: "Channels" })).toBeInTheDocument();
    // ⚠ The old assertion dug into `fetchMock.mock.calls` for a url SUBSTRING and then checked
    // `{ method: "POST", credentials: "include" }` — i.e. it asserted the TEST STUB was called a
    // certain way. Reaching the route-bound login resolver is the stronger claim; what remains
    // worth asserting is the credential payload the form actually sent.
    expect(logins).toEqual([{ username: "ada", password: "hunter2!" }]);
  });

  it("automatically uses the server-gated dev login", async () => {
    let authed = false;
    let devLogins = 0;
    server.use(
      http.get("*/v1/auth/me", () =>
        authed ? HttpResponse.json(ADMIN) : HttpResponse.json({ title: "Unauthorized" }, { status: 401 }),
      ),
      http.get("*/v1/setup/state", () =>
        HttpResponse.json({ bootstrapped: true, devLogin: true, sso: false }),
      ),
      http.post("*/v1/auth/dev-login", () => {
        devLogins += 1;
        authed = true;
        return HttpResponse.json(ADMIN);
      }),
      ...appHandlers(),
    );

    renderApp("/guide");

    await waitFor(() => expect(devLogins).toBe(1));
    expect(await screen.findByRole("heading", { name: "Channels" })).toBeInTheDocument();
    expect(devLogins).toBe(1);
    expect(screen.queryByLabelText("Username")).not.toBeInTheDocument();
  });
});

// THE POST-LOGIN REDIRECT ROUND TRIP (§11).
//
// ⚠ Nothing covered this before: no test asserted the param is set, that login honours it, or
// that a hostile value is refused — and `wizard.spec.ts:115` asserts `toHaveURL(/\/login/)`, a
// regex that does not constrain the param either. The open redirect below shipped behind that gap.
//
// Assertions are on where the router SETTLED, via `at()` — the same reason the legacy-link block
// below gives: a page can look right and still be at the wrong address.
describe("post-login redirect", () => {
  const at = (router: ReturnType<typeof renderApp>) => router.state.location.href;

  // A real protected path rather than the reported /channels/<id>/filler, so the assertion does
  // not depend on channel fixtures resolving; the mechanism under test is the param round trip,
  // which is route-independent.
  it("remembers the deep link a signed-out visitor asked for", async () => {
    stubAuth(false);
    const router = renderApp("/filler/sources");
    await waitFor(() => expect(at(router)).toBe("/login?redirect=%2Ffiller%2Fsources"));
  });

  it("returns them to that deep link after signing in, not to the home page", async () => {
    stubAuth(false);
    const router = renderApp("/filler/sources");
    await screen.findByLabelText("Username");

    await userEvent.type(screen.getByLabelText("Username"), "ada");
    await userEvent.type(screen.getByLabelText("Password"), "hunter2!");
    await userEvent.click(screen.getByRole("button", { name: /sign in/i }));

    await waitFor(() => expect(at(router)).toBe("/filler/sources"));
  });

  // ⚠ THE OPEN REDIRECT. `/login?redirect=https://evil.example` on an ALREADY-SIGNED-IN browser
  // hit `throw redirect({ href })`, which the router force-commits with `replace: true` — i.e. a
  // window.location.replace() straight off the app, gated only by an http/https scheme check.
  it("refuses an off-site destination and falls back to the home page", async () => {
    stubAuth(true);
    const router = renderApp("/login?redirect=https%3A%2F%2Fevil.example");
    await waitFor(() => expect(at(router)).toBe("/guide"));
  });

  // ⚠ A router-level backslash case was written here and DELETED, because sabotaging the
  // validator left it GREEN — jsdom/memory-history does not commit that href the way a browser
  // would, so it asserted nothing. The backslash rule is covered where it does bite:
  // safe-redirect-path.test.ts, whose backslash cases both go red under the same sabotage.
  // A test that passes with the protection removed is worse than no test.
});

// ⚠ A SERVER HICCUP IS NOT A LOGOUT. The guard used to be a bare `catch {}` over a query with
// retry:false, so ANY failure — a 500, a proxy blip, this app's own self-restart — reported a
// valid session as signed out and bounced the user to /login. RestartWatchProvider is wired into
// that very layout, so an operator restarting from the Dashboard hit it every time.
describe("_authed guard distinguishes a failure from a logout", () => {
  const at = (router: ReturnType<typeof renderApp>) => router.state.location.href;

  it("does not send a user to /login when the identity call fails with a 500", async () => {
    server.use(
      ...appHandlers(),
      http.get("*/v1/auth/me", () => HttpResponse.json({ title: "Boom" }, { status: 500 })),
    );
    const router = renderApp("/guide");

    // Give the guard time to resolve and any redirect to commit before asserting the negative.
    await waitFor(() => expect(at(router)).not.toBe("/login"));
    expect(screen.queryByLabelText("Username")).not.toBeInTheDocument();
  });
});

// LEGACY DEEP LINKS — the bookmark-compatibility promise (V-nav-paths).
//
// ⚠ These exist because the Filler redirect SHIPPED BROKEN and every unit test passed. It threw
// its `redirect` from `validateSearch`, where the router is still parsing the URL and treats the
// throw as a parse failure — so `/filler?tab=sources` rendered a BLANK page at the old address.
// Nothing caught it: no test asserted on the final URL, and rendering at the new path (which the
// reachability suite does) exercises a different code path entirely. Only loading the old URL in
// a browser showed it.
//
// The assertion is on where the router SETTLED, not on what rendered — a page can look fine and
// still be at the wrong address, and a redirect that does not move the URL is not a redirect.
describe("legacy tab links redirect to their new paths", () => {
  // ⚠ `location.href`, NOT `pathname + search`. TanStack types `location.search` as the PARSED
  // object, so concatenating it threw "Cannot convert object to primitive value" — on every
  // case, including `/queue`, which has no redirect at all. Five identical failures that all
  // pointed at the app were entirely this helper.
  const at = (router: ReturnType<typeof renderApp>) => router.state.location.href;

  it("/filler?tab=sources lands on /filler/sources", async () => {
    stubAuth(true);
    const router = renderApp("/filler?tab=sources");
    await waitFor(() => expect(at(router)).toBe("/filler/sources"));
  });

  it("/filler?tab=incoming lands on /filler/incoming", async () => {
    stubAuth(true);
    const router = renderApp("/filler?tab=incoming");
    await waitFor(() => expect(at(router)).toBe("/filler/incoming"));
  });

  it("/filler/attention redirects to /filler/incoming", async () => {
    stubAuth(true);
    const router = renderApp("/filler/attention");
    await waitFor(() => expect(at(router)).toBe("/filler/incoming"));
  });

  // ⚠ The catalog FILTERS survive the move. They are query params on purpose — a shared link to
  // a searched view has to keep working — so a redirect that dropped them would silently change
  // what the recipient sees.
  it("carries the catalog filters through the redirect", async () => {
    stubAuth(true);
    const router = renderApp("/filler?tab=sources&q=coke&view=list");
    await waitFor(() => expect(at(router)).toContain("/filler/sources"));
    expect(at(router)).toContain("q=coke");
    expect(at(router)).toContain("view=list");
  });

  it("/channels/ch-1?section=filler lands on the filler section", async () => {
    stubAuth(true);
    const router = renderApp("/channels/ch-1?section=filler");
    await waitFor(() => expect(at(router)).toBe("/channels/ch-1/filler"));
  });

  // `/queue` has no legacy param to translate — it RESOLVES a default instead, which is the same
  // promise seen from the other side: a bare link must land somewhere real.
  it("/queue resolves to a real section", async () => {
    stubAuth(true);
    const router = renderApp("/queue");
    await waitFor(() => expect(at(router)).toMatch(/^\/queue\/(approval|flight)$/));
  });
});
