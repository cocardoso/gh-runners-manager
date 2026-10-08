import { en } from "./en";
import { tr, type Key } from "./index";

/** The label of a known state, status, result or level; an unknown one shows as it is. */
export function stateLabel(value: string): string {
  return Object.hasOwn(en.shell.state, value) ? tr(`shell.state.${value}` as Key) : value;
}
