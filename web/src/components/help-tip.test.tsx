import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "@/i18n";
import { HelpLabel } from "./help-tip";

test("a tap on the help button shows the help, as a phone has no hover", async () => {
  const user = userEvent.setup();
  render(
    <I18nProvider initial="en">
      <HelpLabel label="Name" help="Where the name is used." />
    </I18nProvider>,
  );
  await user.click(screen.getByRole("button", { name: "More information" }));
  expect(await screen.findByText("Where the name is used.")).toBeVisible();
});
