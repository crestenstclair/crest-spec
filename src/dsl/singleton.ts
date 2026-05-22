import type { ProjectBuilder } from "./project-builder.js";

let activeProject: ProjectBuilder | null = null;

export function setActiveProject(project: ProjectBuilder): void {
  activeProject = project;
}

export function getActiveProject(): ProjectBuilder | null {
  return activeProject;
}

export function resetSingleton(): void {
  activeProject = null;
}
