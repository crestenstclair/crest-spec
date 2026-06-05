import { project, command, event, invariant, layer } from "../src/index.js";

const app = project("test-library", {
  layers: ["domain", "application"],
  rules: [
    layer("domain").dependsOn([]),
    layer("application").dependsOn(["domain"]),
  ],
  meta: {
    language: "typescript",
    style: "functional-ish TypeScript; prefer readonly, type aliases for value objects",
    avoid: ["classes with mutable state", "any"],
    typeCheckCommand: ["bun", "x", "tsc", "--noEmit", "--pretty"],
    testCommand: ["bun", "test", "tests/"],
  },
});

// ── Kernel ──────────────────────────────────────────────

const kernel = app.context("Kernel", { purpose: "shared primitives" });

kernel.valueObject("Email", {
  from: "string",
  invariants: ["must contain @", "max 254 chars"],
});

kernel.valueObject("UserId", {
  from: "string",
  description: "UUID v4 identifier for a user",
});

// ── Catalog ─────────────────────────────────────────────

const catalog = app.context("Catalog", { purpose: "product catalog management" });

const product = catalog.aggregate("Product", {
  root: true,
  state: { id: "ProductId", name: "string", price: "number", active: "boolean" },
  invariants: ["price must be positive", "name must not be empty"],
  commands: [
    command("CreateProduct", { name: "string", price: "number" }),
    command("UpdatePrice", { newPrice: "number" }),
    command("Deactivate", {}),
  ],
  events: [
    event("ProductCreated", { id: "ProductId", name: "string", price: "number" }),
    event("PriceUpdated", { id: "ProductId", oldPrice: "number", newPrice: "number" }),
    event("ProductDeactivated", { id: "ProductId" }),
  ],
});

catalog.valueObject("ProductId", {
  from: "string",
  description: "UUID identifier for a product",
});

catalog.repository("ProductRepository", {
  of: { id: product.id, name: "Product" },
  contract: { findById: "ProductId -> Product | null", save: "Product -> void" },
});

catalog.applicationService("CatalogService", {
  purpose: "orchestrates product commands",
  uses: [{ id: product.id, name: "Product" }],
  operations: [
    { name: "createProduct", input: { name: "string", price: "number" } },
    { name: "updatePrice", input: { productId: "string", newPrice: "number" } },
  ],
});

// ── Invariants ──────────────────────────────────────────

app.invariants([
  invariant("domain layer has no infrastructure imports", {
    meta: { rationale: "clean architecture" },
  }),
]);
