import { EnterOutlined } from "@ant-design/icons";
import { useClickAway } from "ahooks";
import { Input, InputRef } from "antd";
import clsx from "clsx";
import { FC, useEffect, useRef, useState } from "react";

import { sessionCoordinator } from "@/core/session/browser";
import { feedbackToast } from "@/utils/common";

import edit_name from "@/assets/images/chatSetting/edit_name.png";

interface IEditableContentProps {
  editable?: boolean;
  value?: string;
  placeholder?: string;
  className?: string;
  textClassName?: string;
  // Explicit false keeps the draft. Historical void callbacks remain success.
  onChange?: (value: string) => void | boolean | Promise<void | boolean>;
  editIdentity?: string;
  captureEdit?: () => { current: () => boolean } | undefined;
}

const EditableContent: FC<IEditableContentProps> = ({
  editable,
  value,
  placeholder,
  className,
  textClassName,
  onChange,
  editIdentity,
  captureEdit,
}) => {
  const wrapRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<InputRef>(null);
  const mounted = useRef(true);
  const epoch = useRef(0);
  const busy = useRef<object | undefined>(undefined);
  const latest = useRef({ editable, value, editIdentity });
  latest.current = { editable, value, editIdentity };
  const editing = useRef<{ epoch: number; identity: string | undefined;
    owner: typeof sessionCoordinator.current; generation: number; actor: string | undefined;
    issuer: string | undefined; tenant: string | undefined;
    view: { current: () => boolean } | undefined; save: IEditableContentProps["onChange"] } | undefined>(undefined);
  const [editState, setEditState] = useState({
    isEdit: false,
    loading: false,
    innerValue: value,
  });
  const current = (edit: NonNullable<typeof editing.current>) =>
    mounted.current && editing.current === edit && epoch.current === edit.epoch &&
    latest.current.editIdentity === edit.identity && sessionCoordinator.current === edit.owner &&
    sessionCoordinator.generation === edit.generation && edit.owner?.userID === edit.actor &&
    edit.owner?.issuer === edit.issuer && edit.owner?.tenant.serverID === edit.tenant &&
    (!edit.view || edit.view.current());
  const cancel = () => {
    ++epoch.current; editing.current = undefined; busy.current = undefined;
    setEditState({ isEdit: false, loading: false, innerValue: latest.current.value });
  };
  useEffect(() => {
    mounted.current = true;
    const dispose = sessionCoordinator.onChange(cancel);
    return () => { mounted.current = false; ++epoch.current; editing.current = undefined; dispose(); };
  }, []);
  useEffect(() => { cancel(); }, [editIdentity]);
  useEffect(() => {
    const edit = editing.current;
    if (edit && !current(edit)) cancel();
  });
  const renderedEdit = editing.current;

  useClickAway(() => {
    if (editState.isEdit) {
      cancel();
    }
  }, [wrapRef]);

  const toggleEdit = (e: React.MouseEvent<HTMLImageElement, MouseEvent>) => {
    e.stopPropagation();
    if (!latest.current.editable) return;
    const view = captureEdit?.();
    if (captureEdit && !view?.current()) return;
    const owner = sessionCoordinator.current;
    const edit = { epoch: ++epoch.current, identity: editIdentity, owner,
      generation: sessionCoordinator.generation, actor: owner?.userID, issuer: owner?.issuer,
      tenant: owner?.tenant.serverID, view, save: onChange };
    editing.current = edit; busy.current = undefined;
    setEditState({
      isEdit: true,
      loading: false,
      innerValue: value === "-" ? "" : value,
    });
    setTimeout(() => { if (current(edit)) inputRef.current?.focus(); });
  };

  const onPressEnter = async (
    e: React.KeyboardEvent<HTMLInputElement> & { target: { value: string } },
  ) => {
    const edit = renderedEdit;
    if (!edit || !current(edit) || !latest.current.editable || busy.current) return;
    const draft = e.target.value; // Never read a mutable event target after await.
    const submission = {}; busy.current = submission;
    setEditState((state) => ({ ...state, innerValue: draft, loading: true }));
    try {
      const saved = await edit.save?.(draft);
      if (!current(edit) || saved === false) return;
      ++epoch.current; editing.current = undefined;
      setEditState({ isEdit: false, loading: false, innerValue: draft });
    } catch (error) {
      if (current(edit)) feedbackToast({ error });
    } finally {
      if (busy.current === submission) busy.current = undefined;
      if (current(edit)) setEditState((state) => ({ ...state, loading: false }));
    }
  };

  return (
    <div ref={wrapRef} className={clsx("ml-3 flex items-center", className)}>
      {editState.isEdit ? (
        <Input
          spellCheck={false}
          value={editState.innerValue}
          placeholder={placeholder}
          maxLength={255}
          disabled={editState.loading || !editable}
          aria-busy={editState.loading}
          onChange={(e) => {
            if (renderedEdit && current(renderedEdit) && !busy.current)
              setEditState((state) => ({ ...state, innerValue: e.target.value }));
          }}
          ref={inputRef}
          onPressEnter={onPressEnter}
          suffix={<EnterOutlined rev={undefined} />}
        />
      ) : (
        <>
          <div className={clsx("mr-1 max-w-[240px] truncate", textClassName)}>
            {value}
          </div>
          {editable && (
            <img
              className="cursor-pointer"
              width={14}
              src={edit_name}
              alt="edit name"
              onClick={toggleEdit}
            />
          )}
        </>
      )}
    </div>
  );
};

export default EditableContent;
