import { render } from "@testing-library/react";
import { withNode } from "./nodes";

test("an element goes where its placeholder is", () => {
  const { container } = render(<p>{withNode("waiting since {time}, still", "{time}", <b>5 min ago</b>)}</p>);
  expect(container.innerHTML).toBe("<p>waiting since <b>5 min ago</b>, still</p>");
});

test("a translation without the placeholder still shows the element, after the text", () => {
  const { container } = render(<p>{withNode("waiting", "{time}", <b>now</b>)}</p>);
  expect(container.innerHTML).toBe("<p>waiting <b>now</b></p>");
});
