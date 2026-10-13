import { useDocumentTitle } from "../hooks/useDocumentTitle";
import { LinkButton } from "../components/ui/button";
import { EmptyState } from "../components/ui/feedback";

export function NotFoundPage() {
  useDocumentTitle("Not found");
  return (
    <EmptyState
      title="Page not found"
      action={
        <LinkButton to="/" variant="primary">
          Go to the overview
        </LinkButton>
      }
    >
      There is nothing at this address. It may have been a request or endpoint that was deleted.
    </EmptyState>
  );
}
