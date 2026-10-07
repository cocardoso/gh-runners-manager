import { render, screen } from "@testing-library/react";
import { EnvironmentStateBadge, JobStatusBadge, LevelBadge, toneFor } from "./status-badge";

test("known states get a tone", () => {
  expect(toneFor("environment", "running")).toBe("info");
  expect(toneFor("environment", "failed")).toBe("error");
  expect(toneFor("result", "succeeded")).toBe("success");
});

test("unknown values render as a neutral badge instead of crashing", () => {
  expect(toneFor("environment", "hibernating")).toBe("neutral");
  render(<EnvironmentStateBadge state="hibernating" />);
  expect(screen.getByText("hibernating")).toBeInTheDocument();
  render(<LevelBadge level="mystery" />);
  expect(screen.getByText("mystery")).toBeInTheDocument();
});

test("a job shows its result once completed", () => {
  render(<JobStatusBadge status="completed" result="failed" />);
  expect(screen.getByText("failed")).toBeInTheDocument();
  render(<JobStatusBadge status="running" />);
  expect(screen.getByText("running")).toBeInTheDocument();
});
