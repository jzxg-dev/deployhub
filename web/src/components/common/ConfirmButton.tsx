import { useState, type ReactNode } from "react";
import { toast } from "sonner";
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { errorMessage } from "@/lib/api";

interface Props {
  trigger: ReactNode;
  title: string;
  description: ReactNode;
  confirmText?: string;
  destructive?: boolean;
  /** 返回 Promise 时等它完成再关闭；出错时提示并保持打开 */
  onConfirm: () => unknown | Promise<unknown>;
}

/** 需要二次确认的操作，如删除。 */
export function ConfirmButton({ trigger, title, description, confirmText = "确定", destructive, onConfirm }: Props) {
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const run = async () => {
    setBusy(true);
    try {
      await onConfirm();
      setOpen(false);
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <AlertDialog open={open} onOpenChange={setOpen}>
      <AlertDialogTrigger asChild>{trigger}</AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription>{description}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={busy}>取消</AlertDialogCancel>
          <Button variant={destructive ? "destructive" : "default"} disabled={busy} onClick={run}>
            {busy ? "处理中…" : confirmText}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
