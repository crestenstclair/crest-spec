const RESET = "\x1b[0m";
const GREEN = "\x1b[32m";
const YELLOW = "\x1b[33m";
const RED = "\x1b[31m";
const CYAN = "\x1b[36m";
const DIM = "\x1b[2m";

export class Formatter {
  static create(text: string): string {
    return `${GREEN}+ ${text}${RESET}`;
  }

  static modify(text: string): string {
    return `${YELLOW}~ ${text}${RESET}`;
  }

  static destroy(text: string): string {
    return `${RED}- ${text}${RESET}`;
  }

  static refresh(text: string): string {
    return `${CYAN}? ${text}${RESET}`;
  }

  static dim(text: string): string {
    return `${DIM}${text}${RESET}`;
  }

  static error(text: string): string {
    return `${RED}Error: ${text}${RESET}`;
  }

  static success(text: string): string {
    return `${GREEN}${text}${RESET}`;
  }
}
