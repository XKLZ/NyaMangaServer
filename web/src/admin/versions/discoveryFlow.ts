import type { WorkProposal } from "../../shared/workDiscovery";
export type ProposalDraft = { baseline: WorkProposal; selected: string[] };
export function sameMembers(a: string[], b: string[]) {
  const left = [...a].sort(),
    right = [...b].sort();
  return left.length === right.length && left.every((v, i) => v === right[i]);
}
export function draftFor(p: WorkProposal): ProposalDraft {
  return { baseline: p, selected: [...p.new_edition_ids] };
}
export function selectionDirty(d: ProposalDraft) {
  return !sameMembers(d.selected, d.baseline.new_edition_ids);
}
export function canResolve(d: ProposalDraft) {
  return (
    new Set(d.selected).size === d.selected.length &&
    d.selected.length >= (d.baseline.kind === "create_work" ? 2 : 1) &&
    d.selected.every((id) => d.baseline.new_edition_ids.includes(id))
  );
}
export function discoverySupported(
  capability: { discovery?: boolean; proposals?: boolean } | null,
) {
  return capability?.discovery === true && capability?.proposals === true;
}
export function pageChanged(a: WorkProposal[], b: WorkProposal[]) {
  return (
    a.length !== b.length ||
    a.some(
      (p, i) =>
        p.id !== b[i]?.id ||
        p.revision !== b[i]?.revision ||
        p.state !== b[i]?.state ||
        p.primary !== b[i]?.primary,
    )
  );
}

/** Ignore older poll responses; newer state never silently rebases a selection. */
export function proposalChanged(
  incoming: WorkProposal,
  baseline: WorkProposal,
) {
  return (
    incoming.revision > baseline.revision ||
    (incoming.revision === baseline.revision &&
      incoming.state !== baseline.state)
  );
}
