import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const sourcePath = path.resolve(
  scriptDir,
  "../src/pages/instances/CreateInstancePage.tsx",
);
const source = readFileSync(sourcePath, "utf8");

function sectionBetween(startMarker, endMarker) {
  const start = source.indexOf(startMarker);
  const end = source.indexOf(endMarker);
  if (start === -1 || end === -1 || end <= start) {
    throw new Error(`Unable to locate section: ${startMarker}`);
  }
  return source.slice(start, end);
}

function assert(condition, message) {
  if (!condition) {
    throw new Error(message);
  }
}

const stepOne = sectionBetween(
  "{/* Step 1: Basic Information */}",
  "{/* Step 2: Select Type */}",
);
const stepTwo = sectionBetween(
  "{/* Step 2: Select Type */}",
  "{/* Step 3: Configuration */}",
);
const stepThree = source.slice(source.indexOf("{/* Step 3: Configuration */}"));

assert(
  /id:\s*"lite"/.test(source) &&
    /id:\s*"isolated"/.test(source) &&
    /id:\s*"pro"/.test(source),
  "Create instance page must define Lite, Isolated, and Pro mode options.",
);
assert(
  stepOne.includes("renderInstanceModeSelector()"),
  "Lite/Pro selector must be visible in step 1.",
);
assert(
  !stepTwo.includes("renderInstanceModeSelector()"),
  "Lite/Pro selector should not be hidden behind step 2.",
);
assert(
  source.includes('const usesDedicatedResources = selectedMode !== "lite";'),
  "Create page must distinguish Lite shared resources from dedicated modes.",
);
assert(
  source.includes('const showRuntimeImageSelector = selectedMode === "pro";'),
  "Runtime image selector visibility must be controlled by Pro mode only.",
);
assert(
  stepTwo.includes("{showRuntimeImageSelector && ("),
  "Step 2 runtime image selector must be hidden for Lite mode.",
);
assert(
  stepThree.includes("{usesDedicatedResources && ("),
  "Step 3 resource controls must be hidden for Lite mode.",
);
assert(
  stepThree.includes("{showRuntimeImageSelector && ("),
  "Step 3 summary must hide runtime image details for Lite mode.",
);
assert(
  source.includes("...(usesDedicatedResources"),
  "Quota validation must include CPU/memory/storage/GPU only for Pro mode.",
);

console.log("Create instance mode selector placement is valid.");
