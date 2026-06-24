import { PageHeader } from "./page";

export function Placeholder({ name }: { name: string }) {
  return <PageHeader title={name} description="This section is being wired up." />;
}
