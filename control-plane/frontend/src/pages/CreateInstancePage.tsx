import { useNavigate } from "react-router-dom";
import { ArrowLeft } from "lucide-react";
import { errorToast } from "@/utils/toast";
import InstanceForm from "@/components/InstanceForm";
import { useCreateInstance } from "@/hooks/useInstances";


export default function CreateInstancePage() {
  const navigate = useNavigate();
  const createMutation = useCreateInstance();

  return (
    <div>
      <button
        onClick={() => navigate("/")}
        className="inline-flex items-center gap-1 text-sm text-gray-600 hover:text-gray-900 mb-4"
      >
        <ArrowLeft size={16} />
        Back to Dashboard
      </button>

      <h1 className="text-xl font-semibold text-gray-900 mb-6">
        Create Instance
      </h1>

      <div className="max-w-2xl">
        <InstanceForm
          onSubmit={(payload) =>
            createMutation.mutate(payload, {
              onSuccess: () => {
                navigate("/");
              },
              onError: (error: any) => {
                if (error.response?.status === 409) {
                  errorToast("Failed to create instance", "An instance with the same name already exists");
                } else {
                  errorToast("Failed to create instance", error);
                }
              },
            })
          }
          onCancel={() => navigate("/")}
          loading={createMutation.isPending}
        />
      </div>
    </div>
  );
}
