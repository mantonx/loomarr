import type { ReactElement, ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({ render: vi.fn(), toaster: vi.fn() }));

vi.mock("@/diagnostics/client-reporter", () => ({
  clientDiagnostics: { record: vi.fn() },
  installGlobalClientDiagnostics: vi.fn(),
}));
vi.mock("@/routeTree.gen", () => ({ routeTree: {} }));
vi.mock("@loomarr/design-system", () => ({
  LoomarrProvider: ({ children }: { children: ReactNode }) => children,
}));
vi.mock("@tanstack/react-query", () => ({
  QueryClient: class QueryClient {},
  QueryClientProvider: ({ children }: { children: ReactNode }) => children,
}));
vi.mock("@tanstack/react-router", () => ({
  createRouter: vi.fn(() => ({})),
  RouterProvider: () => null,
}));
vi.mock("react-dom/client", () => ({ createRoot: () => ({ render: mocks.render }) }));
vi.mock("sonner", () => ({ Toaster: mocks.toaster }));

const findToaster = (node: ReactNode): ReactElement<{ position?: string }> | undefined => {
  if (!node || typeof node !== "object") return undefined;
  const element = node as ReactElement<{ children?: ReactNode; position?: string }>;
  if (element.type === mocks.toaster) return element;
  const children = element.props?.children;
  return Array.isArray(children)
    ? children.map(findToaster).find((child) => child !== undefined)
    : findToaster(children);
};

describe("startup notice layout", () => {
  beforeEach(() => {
    vi.resetModules();
    mocks.render.mockReset();
  });

  it("places persistent health notices away from the bottom Settings commit bar", async () => {
    await import("@/main");

    const app = mocks.render.mock.calls[0]?.[0] as ReactNode;
    expect(findToaster(app)?.props.position).toBe("top-right");
  });
});
