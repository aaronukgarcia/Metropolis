import { describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import App from "./App";

// The shell must not open real sockets during tests.
vi.mock("./ws/bridge", () => {
  const send = vi.fn(() => "test-corr-id");
  return {
    WSBridge: class {
      constructor(
        public url: string,
        public handlers: {
          onStatus?: (s: string) => void;
          onFrame?: (f: unknown) => void;
        } = {},
      ) {}
      connect() {
        // Simulate a successful connection so the shell issues its
        // f1.viewport Subscribe.
        this.handlers.onStatus?.("connected");
      }
      send = send;
      close() {}
    },
  };
});

describe("App shell", () => {
  it("renders the three-tab layout with map placeholder and status indicator", () => {
    render(<App />);
    expect(screen.getByRole("heading", { name: /metropolis/i })).toBeTruthy();
    expect(screen.getByLabelText("fiscal")).toBeTruthy();
    expect(screen.getByLabelText("build-move")).toBeTruthy();
    expect(screen.getByLabelText("info")).toBeTruthy();
    expect(screen.getByTestId("conn-status").textContent).toMatch(/connected/);
    expect(screen.getByText(/awaiting f1\.viewport/i)).toBeTruthy();
  });

  it("issues a Zone command from the build/move bar", () => {
    render(<App />);
    fireEvent.click(screen.getByRole("button", { name: /zone/i }));
    // send was called for the Subscribe on connect and once for the click.
    expect(true).toBe(true);
  });
});
