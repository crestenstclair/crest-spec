export interface IResponseParser {
  parse(response: string): Map<string, string>;
}

export class ResponseParser implements IResponseParser {
  private static readonly BLOCK_REGEX = /```[\w]*\n([\s\S]*?)```/g;
  private static readonly PATH_REGEX = /^\/\/\s*path:\s*(.+)$/m;

  parse(response: string): Map<string, string> {
    const files = new Map<string, string>();

    for (const match of response.matchAll(ResponseParser.BLOCK_REGEX)) {
      const blockContent = match[1];
      const pathMatch = blockContent.match(ResponseParser.PATH_REGEX);
      if (!pathMatch) continue;

      const filePath = pathMatch[1].trim();
      const content = blockContent.replace(ResponseParser.PATH_REGEX, "").trim() + "\n";
      files.set(filePath, content);
    }

    return files;
  }
}
