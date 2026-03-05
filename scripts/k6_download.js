import http from "k6/http";
import { check, sleep } from "k6";

const target = __ENV.TARGET_URL || "http://load.local/payload_100mb.bin";

export const options = {
  discardResponseBodies: true,
  noConnectionReuse: false,
  noVUConnectionReuse: false,
  insecureSkipTLSVerify: true,
};

export default function () {
  const res = http.get(target, { timeout: "120s" });
  check(res, { "status is 200": (r) => r.status === 200 });
  sleep(0.1);
}
