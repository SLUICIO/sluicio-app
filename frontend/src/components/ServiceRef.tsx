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

export default function ServiceRef({ name, className, suffix = "", children }: Props) {
  const canOpen = useCanOpenServices();
  const body = children ?? name;
  if (!canOpen) {
    return (
      <span className={className} title={name}>
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
