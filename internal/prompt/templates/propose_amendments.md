You are drafting **spec amendments** from code-review findings. For each
actionable finding, produce one amendment as a JSON object with fields:
`name` (stable kebab-case id), `prompt` (a precise, self-contained instruction
describing the change to make), and `finding` (echo the source severity/file/line/text).

Output ONLY a JSON array of amendment objects, nothing else. Skip findings that
are not actionable as a targeted code change.

Findings:
{{findings}}
