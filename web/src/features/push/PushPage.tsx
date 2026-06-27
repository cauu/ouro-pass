import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useForm } from "react-hook-form";
import { createPushJob, listPushJobs } from "@/api/admin";
import { ApiError } from "@/api/client";
import { PageHeader, QueryState } from "@/app/page";
import { fmtTime } from "@/lib/format";
import type { PushCreate } from "@/lib/types";
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
      toast({ title: "Push job scheduled", variant: "success" });
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
              Schedule
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

export function PushPage() {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["push-jobs"], queryFn: listPushJobs });
  const jobs = q.data?.jobs ?? [];

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
            </TR>
          </THead>
          <TBody>
            {jobs.map((j) => (
              <TR key={j.JobID}>
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
              </TR>
            ))}
          </TBody>
        </Table>
      </QueryState>
    </>
  );
}
