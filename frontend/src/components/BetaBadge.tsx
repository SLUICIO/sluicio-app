// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// BetaBadge marks a feature that is not yet held to the same promises as
// the rest of the product: collector management rests on OpAMP, whose
// specification is Beta and whose supervisor and collector extension are
// Alpha. One component so every surface of a Beta feature says it the
// same way.

export default function BetaBadge({
  title = "Beta: this feature may change and is not covered by availability commitments.",
}: {
  title?: string;
}) {
  return (
    <span
      className="badge-brand"
      title={title}
      style={{ marginLeft: 8, verticalAlign: "middle" }}
    >
      Beta
    </span>
  );
}
