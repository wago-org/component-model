use std::collections::HashMap;
use std::env;
use std::fs;
use std::path::{Path, PathBuf};

use anyhow::{Context, Result, bail};
use serde::Serialize;
use wast::component::WastVal;
use wast::parser::{ParseBuffer, parse};
use wast::{QuoteWat, Wast, WastArg, WastDirective, WastExecute, WastRet};

#[derive(Serialize)]
struct Manifest {
    revision: String,
    files: Vec<SuiteFile>,
}

#[derive(Serialize)]
struct SuiteFile {
    source: String,
    cases: Vec<Case>,
}

#[derive(Serialize)]
struct Case {
    id: String,
    line: usize,
    kind: String,
    wasm: Option<String>,
    message: Option<String>,
    generation_error: Option<String>,
    actions: Vec<Action>,
}

#[derive(Serialize)]
struct Action {
    line: usize,
    kind: String,
    export: String,
    args: Vec<Value>,
    results: Vec<Value>,
    message: Option<String>,
    generation_error: Option<String>,
}

#[derive(Serialize)]
#[serde(tag = "kind", content = "value", rename_all = "kebab-case")]
enum Value {
    Bool(bool),
    U8(u8),
    S8(i8),
    U16(u16),
    S16(i16),
    U32(u32),
    S32(i32),
    U64(String),
    S64(String),
    F32(String),
    F64(String),
    Char(String),
    String(String),
    List(Vec<Value>),
    Record(Vec<RecordField>),
    Tuple(Vec<Value>),
    Variant(VariantValue),
    Enum(String),
    Option(Option<Box<Value>>),
    Result(ResultValue),
    Flags(Vec<String>),
}

#[derive(Serialize)]
struct RecordField {
    name: String,
    value: Value,
}

#[derive(Serialize)]
struct VariantValue {
    case: String,
    payload: Option<Box<Value>>,
}

#[derive(Serialize)]
struct ResultValue {
    is_err: bool,
    payload: Option<Box<Value>>,
}

fn main() -> Result<()> {
    let mut args = env::args_os().skip(1);
    let input = PathBuf::from(
        args.next()
            .context("usage: wast2manifest INPUT OUTPUT REVISION")?,
    );
    let output = PathBuf::from(
        args.next()
            .context("usage: wast2manifest INPUT OUTPUT REVISION")?,
    );
    let revision = args
        .next()
        .context("usage: wast2manifest INPUT OUTPUT REVISION")?
        .to_string_lossy()
        .into_owned();
    if args.next().is_some() {
        bail!("usage: wast2manifest INPUT OUTPUT REVISION");
    }

    let generated = output.join("generated");
    if generated.exists() {
        fs::remove_dir_all(&generated)?;
    }
    fs::create_dir_all(&generated)?;
    let mut sources = Vec::new();
    collect_wast(&input, &mut sources)?;
    sources.sort();

    let mut files = Vec::with_capacity(sources.len());
    for source in sources {
        files.push(convert_file(&input, &source, &output)?);
    }
    let manifest = Manifest { revision, files };
    fs::write(
        output.join("manifest.json"),
        serde_json::to_vec_pretty(&manifest)?,
    )?;
    Ok(())
}

fn collect_wast(dir: &Path, out: &mut Vec<PathBuf>) -> Result<()> {
    for entry in fs::read_dir(dir).with_context(|| format!("read {}", dir.display()))? {
        let path = entry?.path();
        if path.is_dir() {
            collect_wast(&path, out)?;
        } else if path.extension().is_some_and(|ext| ext == "wast") {
            out.push(path);
        }
    }
    Ok(())
}

fn convert_file(root: &Path, source_path: &Path, output: &Path) -> Result<SuiteFile> {
    let source = fs::read_to_string(source_path)?;
    let relative = source_path
        .strip_prefix(root)?
        .to_string_lossy()
        .replace('\\', "/");
    let buffer = ParseBuffer::new(&source).with_context(|| relative.clone())?;
    let wast = parse::<Wast<'_>>(&buffer).with_context(|| relative.clone())?;
    let mut definitions = HashMap::<String, String>::new();
    let mut cases = Vec::<Case>::new();
    let mut active = None::<usize>;

    for (directive_index, directive) in wast.directives.into_iter().enumerate() {
        let line = directive.span().linecol_in(&source).0 + 1;
        match directive {
            WastDirective::Module(mut module) => {
                let name = module.name().map(|id| id.name().to_string());
                let case = encoded_case(
                    &relative,
                    directive_index,
                    line,
                    "instantiate",
                    &mut module,
                    None,
                    output,
                );
                cases.push(case?);
                active = Some(cases.len() - 1);
                if let (Some(name), Some(wasm)) =
                    (name, cases.last().and_then(|case| case.wasm.clone()))
                {
                    definitions.insert(name, wasm);
                }
            }
            WastDirective::ModuleDefinition(mut module) => {
                let name = module.name().map(|id| id.name().to_string());
                let case = encoded_case(
                    &relative,
                    directive_index,
                    line,
                    "definition",
                    &mut module,
                    None,
                    output,
                );
                cases.push(case?);
                active = None;
                if let (Some(name), Some(wasm)) =
                    (name, cases.last().and_then(|case| case.wasm.clone()))
                {
                    definitions.insert(name, wasm);
                }
            }
            WastDirective::ModuleInstance { module, .. } => {
                let id = case_id(&relative, directive_index, line);
                let wasm = module.and_then(|id| definitions.get(id.name()).cloned());
                let generation_error = wasm.is_none().then(|| {
                    "component instance references an unknown or unnamed definition".to_string()
                });
                cases.push(Case {
                    id,
                    line,
                    kind: "instantiate".to_string(),
                    wasm,
                    message: None,
                    generation_error,
                    actions: Vec::new(),
                });
                active = Some(cases.len() - 1);
            }
            WastDirective::AssertInvalid {
                mut module,
                message,
                ..
            } => {
                cases.push(encoded_case(
                    &relative,
                    directive_index,
                    line,
                    "assert-invalid",
                    &mut module,
                    Some(message),
                    output,
                )?);
            }
            WastDirective::AssertMalformed {
                mut module,
                message,
                ..
            } => {
                cases.push(encoded_case(
                    &relative,
                    directive_index,
                    line,
                    "assert-malformed",
                    &mut module,
                    Some(message),
                    output,
                )?);
            }
            WastDirective::AssertUnlinkable {
                module, message, ..
            } => {
                let mut module = QuoteWat::Wat(module);
                cases.push(encoded_case(
                    &relative,
                    directive_index,
                    line,
                    "assert-unlinkable",
                    &mut module,
                    Some(message),
                    output,
                )?);
            }
            WastDirective::AssertReturn { exec, results, .. } => match exec {
                WastExecute::Invoke(_) => push_action(
                    &mut cases,
                    active,
                    action(line, "assert-return", exec, results, None),
                ),
                other => cases.push(unsupported_case(
                    &relative,
                    directive_index,
                    line,
                    format!("assert-return execution is not an invoke: {other:?}"),
                )),
            },
            WastDirective::AssertTrap { exec, message, .. } => match exec {
                WastExecute::Invoke(_) => push_action(
                    &mut cases,
                    active,
                    action(line, "assert-trap", exec, Vec::new(), Some(message)),
                ),
                WastExecute::Wat(wat) => {
                    let mut module = QuoteWat::Wat(wat);
                    cases.push(encoded_case(
                        &relative,
                        directive_index,
                        line,
                        "assert-instantiation-trap",
                        &mut module,
                        Some(message),
                        output,
                    )?);
                }
                other => cases.push(unsupported_case(
                    &relative,
                    directive_index,
                    line,
                    format!("assert-trap execution is unsupported: {other:?}"),
                )),
            },
            WastDirective::Invoke(invoke) => {
                let exec = WastExecute::Invoke(invoke);
                push_action(
                    &mut cases,
                    active,
                    action(line, "invoke", exec, Vec::new(), None),
                );
            }
            other => {
                cases.push(unsupported_case(
                    &relative,
                    directive_index,
                    line,
                    format!("{other:?}"),
                ));
                active = None;
            }
        }
    }

    Ok(SuiteFile {
        source: relative,
        cases,
    })
}

fn encoded_case(
    source: &str,
    directive_index: usize,
    line: usize,
    kind: &str,
    module: &mut QuoteWat<'_>,
    message: Option<&str>,
    output: &Path,
) -> Result<Case> {
    let id = case_id(source, directive_index, line);
    let (wasm, generation_error) = match module.encode() {
        Ok(bytes) => {
            let relative = format!("generated/{id}.wasm");
            fs::write(output.join(&relative), bytes)?;
            (Some(relative), None)
        }
        Err(error) => (None, Some(error.to_string())),
    };
    Ok(Case {
        id,
        line,
        kind: kind.to_string(),
        wasm,
        message: message.map(str::to_string),
        generation_error,
        actions: Vec::new(),
    })
}

fn case_id(source: &str, directive_index: usize, line: usize) -> String {
    let stem = source
        .trim_end_matches(".wast")
        .replace(['/', '\\'], "__")
        .replace(
            |c: char| !c.is_ascii_alphanumeric() && c != '_' && c != '-',
            "_",
        );
    format!("{stem}__{directive_index:04}__L{line}")
}

fn unsupported_case(source: &str, directive_index: usize, line: usize, error: String) -> Case {
    Case {
        id: case_id(source, directive_index, line),
        line,
        kind: "unsupported-directive".to_string(),
        wasm: None,
        message: None,
        generation_error: Some(error),
        actions: Vec::new(),
    }
}

fn push_action(cases: &mut [Case], active: Option<usize>, result: Result<Action>) {
    let Some(active) = active else {
        // This can only happen for malformed scripts. Official assertions
        // always target the most recently instantiated component.
        return;
    };
    match result {
        Ok(action) => cases[active].actions.push(action),
        Err(error) => cases[active].actions.push(Action {
            line: 0,
            kind: "unsupported-action".to_string(),
            export: String::new(),
            args: Vec::new(),
            results: Vec::new(),
            message: None,
            generation_error: Some(error.to_string()),
        }),
    }
}

fn action(
    line: usize,
    kind: &str,
    exec: WastExecute<'_>,
    results: Vec<WastRet<'_>>,
    message: Option<&str>,
) -> Result<Action> {
    let WastExecute::Invoke(invoke) = exec else {
        bail!("only invoke actions are supported")
    };
    if invoke.module.is_some() {
        bail!("named invoke targets are not supported")
    }
    let args = invoke
        .args
        .into_iter()
        .map(|arg| match arg {
            WastArg::Component(value) => value_from_wast(value),
            _ => bail!("core Wasm arguments are not component values"),
        })
        .collect::<Result<Vec<_>>>()?;
    let results = results
        .into_iter()
        .map(|result| match result {
            WastRet::Component(value) => value_from_wast(value),
            _ => bail!("core Wasm results are not component values"),
        })
        .collect::<Result<Vec<_>>>()?;
    Ok(Action {
        line,
        kind: kind.to_string(),
        export: invoke.name.to_string(),
        args,
        results,
        message: message.map(str::to_string),
        generation_error: None,
    })
}

fn value_from_wast(value: WastVal<'_>) -> Result<Value> {
    Ok(match value {
        WastVal::Bool(value) => Value::Bool(value),
        WastVal::U8(value) => Value::U8(value),
        WastVal::S8(value) => Value::S8(value),
        WastVal::U16(value) => Value::U16(value),
        WastVal::S16(value) => Value::S16(value),
        WastVal::U32(value) => Value::U32(value),
        WastVal::S32(value) => Value::S32(value),
        WastVal::U64(value) => Value::U64(value.to_string()),
        WastVal::S64(value) => Value::S64(value.to_string()),
        WastVal::F32(value) => Value::F32(format!("{:08x}", value.bits)),
        WastVal::F64(value) => Value::F64(format!("{:016x}", value.bits)),
        WastVal::Char(value) => Value::Char(value.to_string()),
        WastVal::String(value) => Value::String(value.to_string()),
        WastVal::List(values) => Value::List(
            values
                .into_iter()
                .map(value_from_wast)
                .collect::<Result<Vec<_>>>()?,
        ),
        WastVal::Record(fields) => Value::Record(
            fields
                .into_iter()
                .map(|(name, value)| {
                    Ok(RecordField {
                        name: name.to_string(),
                        value: value_from_wast(value)?,
                    })
                })
                .collect::<Result<Vec<_>>>()?,
        ),
        WastVal::Tuple(values) => Value::Tuple(
            values
                .into_iter()
                .map(value_from_wast)
                .collect::<Result<Vec<_>>>()?,
        ),
        WastVal::Variant(case, payload) => Value::Variant(VariantValue {
            case: case.to_string(),
            payload: payload
                .map(|value| value_from_wast(*value).map(Box::new))
                .transpose()?,
        }),
        WastVal::Enum(value) => Value::Enum(value.to_string()),
        WastVal::Option(value) => Value::Option(
            value
                .map(|value| value_from_wast(*value).map(Box::new))
                .transpose()?,
        ),
        WastVal::Result(value) => match value {
            Ok(payload) => Value::Result(ResultValue {
                is_err: false,
                payload: payload
                    .map(|value| value_from_wast(*value).map(Box::new))
                    .transpose()?,
            }),
            Err(payload) => Value::Result(ResultValue {
                is_err: true,
                payload: payload
                    .map(|value| value_from_wast(*value).map(Box::new))
                    .transpose()?,
            }),
        },
        WastVal::Flags(values) => Value::Flags(values.into_iter().map(str::to_string).collect()),
    })
}
