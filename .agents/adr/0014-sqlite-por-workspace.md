# 0014. Um banco SQLite por workspace

- Status: aceito
- Data: 2026-11-07

## Contexto

O Kluzo roda numa máquina só (notebook ou VPS), com alguns workspaces ao mesmo tempo. O estado de cada workspace é independente, e o journal cresce com os eventos dos agentes.

## Decisão

- **SQLite** (`modernc.org/sqlite`, sem cgo) com WAL.
- **Um arquivo por workspace** (`workspaces/<projeto>-<milestone>/state.db`), mais um `kluzo.db` global, só com o registro de projetos e workspaces.

## Consequências

- Nenhum serviço de banco para operar; backup e arquivamento de um workspace são cópias de pasta.
- Workspaces não disputam lock de escrita entre si.
- Consultas que cruzam workspaces (Inbox global, custos do dia) leem vários bancos. Isso é aceitável no volume esperado (dezenas de workspaces).
- Sem cgo, o cross-compile continua trivial.
