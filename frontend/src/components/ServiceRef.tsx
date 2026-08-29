// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// A service name that links to its page, or does not (issue #32).
//
// A reader granted an integration but not its services still sees
// service names all over the product: the message list's service
// column, a span's owner, an error's origin. Those names are useful —
// they say which system did the work — but linking them offers a page
// that answers "service not found".
//
// A dead link is worse than plain text. Plain text tells you the name;
// a link tells you the name and then punishes you for believing it.

import { Link } from "react-router-dom";
import { useCanOpenServices } from "../lib/useNavigationReach";

interface Props {
  name: string;
  className?: string;
  /** Appended to the service path, e.g. "?tab=health". */
  suffix?: string;
  children?: React.ReactNode;
}

// Callers style ServiceRef as the link it usually is: underlined, with a
// hover state. Those classes were passed straight through to the
// non-link branch, so a reader who cannot open services got a name that
// was underlined and lit up under the cursor and did nothing at all.
//
// A dead link is worse than plain text; a thing that only LOOKS like a
// dead link is the same insult with an extra step, and it is the harder
// one to notice because nobody clicks twice.
//
// Stripped centrally rather than at the call sites: there are twenty-odd
// of them, they cannot know whether this reader may open services, and
// the next one added would get it wrong again.
const LINK_AFFORDANCE = /^(hover:|group-hover:|focus:)|^(underline|no-underline|underline-offset-\S+|cursor-pointer)$/;

function plainClassName(className?: string): string | undefined {
  if (!className) return className;
  const kept = className.split(/\s+/).filter((c) => c && !LINK_AFFORDANCE.test(c));
  return kept.length > 0 ? kept.join(" ") : undefined;
}

export default function ServiceRef({ name, className, suffix = "", children }: Props) {
  const canOpen = useCanOpenServices();
  const body = children ?? name;
  if (!canOpen) {
    return (
      <span className={plainClassName(className)} title={name}>
        {body}
      </span>
    );
  }
  return (
    <Link className={className} to={`/services/${encodeURIComponent(name)}${suffix}`}>
      {body}
    </Link>
  );
}
