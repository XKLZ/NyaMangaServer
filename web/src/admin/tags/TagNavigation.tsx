import { createContext, useContext } from "react";
export type TagDestination = {
  to: string;
  state: unknown;
  replace: boolean;
  same: boolean;
};
export const TagNavigation = createContext<{
  related: (id: string) => TagDestination;
  stateFor: (search: string) => unknown;
} | null>(null);
export function useTagNavigation() {
  const value = useContext(TagNavigation);
  if (!value) throw Error("Tag navigation context is missing");
  return value;
}
