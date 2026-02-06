import { useState } from "react";
import {
  DndContext,
  DragOverlay,
  PointerSensor,
  useSensor,
  useSensors,
  closestCenter,
  type DragStartEvent,
  type DragEndEvent,
} from "@dnd-kit/core";
import {
  SortableContext,
  verticalListSortingStrategy,
  useSortable,
} from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import InstanceRow from "./InstanceRow";
import type { Instance } from "@/types/instance";

interface InstanceTableProps {
  instances: Instance[];
  onStart: (id: number) => void;
  onStop: (id: number) => void;
  onRestart: (id: number) => void;
  onDelete: (id: number) => void;
  onReorder?: (orderedIds: number[]) => void;
  loadingInstanceId?: number | null;
}

function SortableRow({
  instance,
  onStart,
  onStop,
  onRestart,
  onDelete,
  loading,
}: {
  instance: Instance;
  onStart: (id: number) => void;
  onStop: (id: number) => void;
  onRestart: (id: number) => void;
  onDelete: (id: number) => void;
  loading?: boolean;
}) {
  const {
    attributes,
    listeners,
    setNodeRef,
    transform,
    transition,
    isDragging,
  } = useSortable({ id: instance.id });

  const style = {
    transform: CSS.Transform.toString(transform),
    transition,
    opacity: isDragging ? 0.4 : 1,
  };

  return (
    <tr
      ref={setNodeRef}
      style={style}
      className="border-b border-gray-100 hover:bg-gray-50"
    >
      <InstanceRow
        instance={instance}
        onStart={onStart}
        onStop={onStop}
        onRestart={onRestart}
        onDelete={onDelete}
        loading={loading}
        dragHandleListeners={listeners}
        dragHandleAttributes={attributes}
      />
    </tr>
  );
}

export default function InstanceTable({
  instances,
  onStart,
  onStop,
  onRestart,
  onDelete,
  onReorder,
  loadingInstanceId,
}: InstanceTableProps) {
  const [activeId, setActiveId] = useState<number | null>(null);
  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 8 } }),
  );

  const activeInstance = activeId
    ? instances.find((i) => i.id === activeId)
    : null;

  function handleDragStart(event: DragStartEvent) {
    setActiveId(event.active.id as number);
  }

  function handleDragEnd(event: DragEndEvent) {
    setActiveId(null);
    const { active, over } = event;
    if (!over || active.id === over.id) return;

    const oldIndex = instances.findIndex((i) => i.id === active.id);
    const newIndex = instances.findIndex((i) => i.id === over.id);
    if (oldIndex === -1 || newIndex === -1) return;

    const reordered = [...instances];
    const moved = reordered.splice(oldIndex, 1)[0];
    if (!moved) return;
    reordered.splice(newIndex, 0, moved);
    onReorder?.(reordered.map((i) => i.id));
  }

  return (
    <div className="bg-white rounded-lg border border-gray-200 overflow-hidden">
      <DndContext
        sensors={sensors}
        collisionDetection={closestCenter}
        onDragStart={handleDragStart}
        onDragEnd={handleDragEnd}
      >
        <table className="w-full">
          <thead>
            <tr className="bg-gray-50 border-b border-gray-200">
              <th className="w-8 px-1 py-3" />
              <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                Name
              </th>
              <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                Status
              </th>
              <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                Created
              </th>
              <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                Actions
              </th>
            </tr>
          </thead>
          <tbody>
            <SortableContext
              items={instances.map((i) => i.id)}
              strategy={verticalListSortingStrategy}
            >
              {instances.map((inst) => (
                <SortableRow
                  key={inst.id}
                  instance={inst}
                  onStart={onStart}
                  onStop={onStop}
                  onRestart={onRestart}
                  onDelete={onDelete}
                  loading={loadingInstanceId === inst.id}
                />
              ))}
            </SortableContext>
          </tbody>
        </table>
        <DragOverlay>
          {activeInstance ? (
            <div className="bg-white shadow-lg rounded px-4 py-2 text-sm font-medium text-gray-700 border">
              {activeInstance.display_name}
            </div>
          ) : null}
        </DragOverlay>
      </DndContext>
    </div>
  );
}
