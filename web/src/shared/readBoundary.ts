import { createContext } from "react";
/** A blocked library scope suspends reads without destroying mounted drafts. */
export const BusinessReadContext = createContext(true);
