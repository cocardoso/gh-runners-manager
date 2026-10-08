import type { ReactNode } from "react";

/** Puts node where placeholder sits in a translated text; a translation without the
 * placeholder still shows the node, after the text. */
export function withNode(text: string, placeholder: string, node: ReactNode): ReactNode {
  const at = text.indexOf(placeholder);
  if (at < 0) {
    return (
      <>
        {text} {node}
      </>
    );
  }
  return (
    <>
      {text.slice(0, at)}
      {node}
      {text.slice(at + placeholder.length)}
    </>
  );
}
