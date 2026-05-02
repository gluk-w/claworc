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
  rectSortingStrategy,
  useSortable,
} from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import InstanceCard from "./InstanceCard";
import type { Instance } from "@/types/instance";

interface InstanceGridProps {
  instances: Instance[];
  onStart: (id: number) => void;
  onStop: (id: number) => void;
  onRestart: (id: number) => void;
  onClone: (id: number) => void;
  onDelete: (id: number) => void;
  onReorder?: (orderedIds: number[]) => void;
  loadingInstanceId?: number | null;
}

function SortableCard({
  instance,
  onStart,
  onStop,
  onRestart,
  onClone,
  onDelete,
  loading,
}: {
  instance: Instance;
  onStart: (id: number) => void;
  onStop: (id: number) => void;
  onRestart: (id: number) => void;
  onClone: (id: number) => void;
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
    <div ref={setNodeRef} style={style}>
      <InstanceCard
        instance={instance}
        onStart={onStart}
        onStop={onStop}
        onRestart={onRestart}
        onClone={onClone}
        onDelete={onDelete}
        loading={loading}
        dragHandleListeners={listeners}
        dragHandleAttributes={attributes}
      />
    </div>
  );
}

export default function InstanceGrid({
  instances,
  onStart,
  onStop,
  onRestart,
  onClone,
  onDelete,
  onReorder,
  loadingInstanceId,
}: InstanceGridProps) {
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
    <DndContext
      sensors={sensors}
      collisionDetection={closestCenter}
      onDragStart={handleDragStart}
      onDragEnd={handleDragEnd}
    >
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
        <SortableContext
          items={instances.map((i) => i.id)}
          strategy={rectSortingStrategy}
        >
          {instances.map((inst) => (
            <SortableCard
              key={inst.id}
              instance={inst}
              onStart={onStart}
              onStop={onStop}
              onRestart={onRestart}
              onClone={onClone}
              onDelete={onDelete}
              loading={loadingInstanceId === inst.id}
            />
          ))}
        </SortableContext>
      </div>
      <DragOverlay>
        {activeInstance ? (
          <div className="shadow-lg rounded-xl">
            <InstanceCard
              instance={activeInstance}
              onStart={onStart}
              onStop={onStop}
              onRestart={onRestart}
              onClone={onClone}
              onDelete={onDelete}
            />
          </div>
        ) : null}
      </DragOverlay>
    </DndContext>
  );
}
