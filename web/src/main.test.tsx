import { render, screen } from "@testing-library/react";
import { Root } from "./main";

test("the root renders the app", () => {
  render(<Root />);
  expect(screen.getByText("gh-runners-manager")).toBeInTheDocument();
});
