import axios from "axios";

const client = axios.create({
  baseURL: "/api/v1",
});

client.interceptors.response.use(
  (response) => response,
  (error) => {
    if (
      error.response?.status === 401 &&
      !error.config?.url?.includes("/auth/")
    ) {
      window.location.href = "/login";
    }
    return Promise.reject(error);
  },
);

export default client;
