import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronRight } from "lucide-react";
import { useState } from "react";
import { useForm } from "react-hook-form";
import { createPushJob, listPushJobs } from "@/api/admin";
import { ApiError } from "@/api/client";
import { PageHeader, QueryState } from "@/app/page";
import { fmtTime } from "@/lib/format";
import type { PushCreate, PushJob } from "@/lib/types";
import { Button } from "@/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/ui/dialog";
import {
  Drawer,
  DrawerContent,
  DrawerDescription,
  DrawerHeader,
  DrawerTitle,
} from "@/ui/drawer";
import { Field } from "@/ui/field";
import { Input } from "@/ui/input";
import { StatusBadge } from "@/ui/status-badge";
import { Table, TBody, TD, TH, THead, TR } from "@/ui/table";
import { Textarea } from "@/ui/textarea";
import { useToast } from "@/ui/toast";

interface PushForm {
  title: string;
  content: string;
  channel_type: string;
  tier: string;
  topic: string;
  entitlement: string;
}

function CreatePushDialog({ onCreated }: { onCreated: () => void }) {
  const [open, setOpen] = useState(false);
  const { toast } = useToast();
  const {
    register,
    handleSubmit,
    reset,
    formState: { errors },
  } = useForm<PushForm>({
    defaultValues: { title: "", content: "", channel_type: "telegram", tier: "", topic: "", entitlement: "" },
  });

  const create = useMutation({
    mutationFn: (v: PushForm) => {
      const body: PushCreate = {
        title: v.title,
        content: v.content,
        channel_type: v.channel_type,
        target: {
          ...(v.tier ? { tier: v.tier } : {}),
          ...(v.topic ? { topic: v.topic } : {}),
          ...(v.entitlement ? { entitlement: v.entitlement } : {}),
        },
      };
      return createPushJob(body);
    },
    onSuccess: () => {
      toast({ title: "Push job created", variant: "success" });
      setOpen(false);
      reset();
      onCreated();
    },
    onError: (e) =>
      toast({
        title: "Create failed",
        description: e instanceof ApiError ? e.message : String(e),
        variant: "destructive",
      }),
  });

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button size="sm">New push</Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>New push</DialogTitle>
          <DialogDescription>Target an audience by tier / topic / entitlement (blank = all).</DialogDescription>
        </DialogHeader>
        <form className="grid gap-3" onSubmit={handleSubmit((v) => create.mutate(v))}>
          <Field label="Title" required error={errors.title && "Title is required"}>
            <Input {...register("title", { required: true })} />
          </Field>
          <Field label="Content" required error={errors.content && "Content is required"}>
            <Textarea rows={3} {...register("content", { required: true })} className="font-sans" />
          </Field>
          <div className="grid grid-cols-2 gap-3">
            <Field label="Channel" required>
              <Input {...register("channel_type")} />
            </Field>
            <Field label="Target tier">
              <Input {...register("tier")} placeholder="gold" />
            </Field>
            <Field label="Target topic">
              <Input {...register("topic")} />
            </Field>
            <Field label="Required entitlement">
              <Input {...register("entitlement")} />
            </Field>
          </div>
          <DialogFooter>
            <Button type="submit" disabled={create.isPending}>
              Create push
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

// DetailRow is one label/value line in the push detail drawer.
function DetailRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="grid gap-1">
      <dt className="text-xs font-medium uppercase tracking-wider text-muted-foreground">{label}</dt>
      <dd className="text-sm">{children}</dd>
    </div>
  );
}

// PushDetailDrawer surfaces the full job (S0008 p2-3): the list keeps the title,
// the drawer carries the full content, targeting and provenance — all already in
// the list response.
function PushDetailDrawer({
  job,
  onOpenChange,
}: {
  job: PushJob | null;
  onOpenChange: (open: boolean) => void;
}) {
  const target =
    [
      job?.TargetTier && `tier: ${job.TargetTier}`,
      job?.TargetTopic && `topic: ${job.TargetTopic}`,
      job?.RequiredEntitlement && `entitlement: ${job.RequiredEntitlement}`,
    ]
      .filter(Boolean)
      .join(" · ") || "all members";

  return (
    <Drawer open={!!job} onOpenChange={onOpenChange}>
      <DrawerContent>
        {job ? (
          <>
            <DrawerHeader>
              <DrawerTitle>{job.Title}</DrawerTitle>
              <DrawerDescription>Push job detail</DrawerDescription>
            </DrawerHeader>
            <dl className="grid gap-4">
              <DetailRow label="Status">
                <StatusBadge status={job.Status} />
              </DetailRow>
              <DetailRow label="Channel">{job.ChannelType}</DetailRow>
              <DetailRow label="Target">{target}</DetailRow>
              <DetailRow label="Content">
                <p className="whitespace-pre-wrap rounded-md border bg-surface p-3 text-sm">{job.Content}</p>
              </DetailRow>
              <DetailRow label="Created by">{job.CreatedBy || "—"}</DetailRow>
              <DetailRow label="Created">{fmtTime(job.CreatedAt)}</DetailRow>
              <DetailRow label="Scheduled">{job.ScheduledAt ? fmtTime(job.ScheduledAt) : "sent immediately"}</DetailRow>
            </dl>
          </>
        ) : null}
      </DrawerContent>
    </Drawer>
  );
}

export function PushPage() {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["push-jobs"], queryFn: listPushJobs });
  const jobs = q.data?.jobs ?? [];
  const [selected, setSelected] = useState<PushJob | null>(null);

  return (
    <>
      <PageHeader
        title="Push"
        description="Broadcast jobs and their delivery status."
        action={<CreatePushDialog onCreated={() => qc.invalidateQueries({ queryKey: ["push-jobs"] })} />}
      />
      <QueryState isLoading={q.isLoading} error={q.error} empty={jobs.length === 0} emptyText="No push jobs yet.">
        <Table footer={<span>{jobs.length} job(s)</span>}>
          <THead>
            <TR>
              <TH>Title</TH>
              <TH>Channel</TH>
              <TH>Target</TH>
              <TH>Status</TH>
              <TH>Scheduled</TH>
              <TH className="w-8" />
            </TR>
          </THead>
          <TBody>
            {jobs.map((j) => (
              <TR key={j.JobID} className="cursor-pointer" onClick={() => setSelected(j)}>
                <TD className="font-medium">{j.Title}</TD>
                <TD>{j.ChannelType}</TD>
                <TD className="text-xs text-muted-foreground">
                  {[j.TargetTier && `tier:${j.TargetTier}`, j.TargetTopic && `topic:${j.TargetTopic}`, j.RequiredEntitlement && `ent:${j.RequiredEntitlement}`]
                    .filter(Boolean)
                    .join(" · ") || "all"}
                </TD>
                <TD>
                  <StatusBadge status={j.Status} />
                </TD>
                <TD className="text-muted-foreground">{j.ScheduledAt ? fmtTime(j.ScheduledAt) : "now"}</TD>
                <TD className="text-right text-muted-foreground">
                  <ChevronRight className="h-4 w-4" />
                </TD>
              </TR>
            ))}
          </TBody>
        </Table>
      </QueryState>

      <PushDetailDrawer
        job={selected}
        onOpenChange={(o) => {
          if (!o) setSelected(null);
        }}
      />
    </>
  );
}
