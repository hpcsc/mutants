defmodule Mix.Tasks.Mutants.Test do
  use Mix.Task

  # the runner of mutants reads this exit code as NOT VIABLE
  @not_compiled 97

  @impl true
  def run(args) do
    Application.ensure_loaded(:ex_unit)
    Mix.Task.run("compile", args)

    case System.get_env("MUTANTS_MUTATED") do
      nil -> :ok
      mutated -> load(mutated, System.fetch_env!("MUTANTS_ORIGINAL"))
    end

    coverage = System.get_env("MUTANTS_COVERAGE")
    if coverage, do: measure(coverage)

    Application.put_env(:ex_unit, :failures_manifest_path, System.fetch_env!("MUTANTS_FAILURES"))
    Mix.Task.run("test", args)

    if coverage && not File.exists?(coverage) do
      File.write!(coverage, encode(0, %{}))
    end
  end

  defp load(mutated, original) do
    Code.put_compiler_option(:ignore_module_conflict, true)

    try do
      Code.compile_string(File.read!(mutated), original)
    rescue
      error ->
        IO.puts(:stderr, "mutants: the mutant does not compile: " <> Exception.message(error))
        System.halt(@not_compiled)
    end
  end

  defp measure(output) do
    Mix.ensure_application!(:tools)
    _ = :cover.start()
    _ = :cover.compile_beam_directory(String.to_charlist(Mix.Project.compile_path()))
    project = File.cwd!()

    ExUnit.after_suite(fn result ->
      files =
        for module <- :cover.modules(),
            source = module.module_info(:compile)[:source],
            source != nil,
            path = Path.relative_to(List.to_string(source), project),
            Path.type(path) == :relative,
            {:ok, lines} = :cover.analyse(module, :calls, :line),
            {{_, line}, calls} <- lines,
            line > 0,
            reduce: %{} do
          files -> Map.update(files, path, %{line => calls}, &Map.update(&1, line, calls, fn seen -> seen + calls end))
        end

      tests = result.total - Map.get(result, :excluded, 0) - Map.get(result, :skipped, 0)
      File.write!(output, encode(tests, files))
    end)
  end

  defp encode(tests, files) do
    entries =
      Enum.map_join(files, ",", fn {path, lines} ->
        measured = Enum.sort(Map.keys(lines))
        missing = for line <- measured, lines[line] == 0, do: line
        ~s(#{quoted(path)}: {"lines": [#{Enum.join(measured, ",")}], "missing": [#{Enum.join(missing, ",")}]})
      end)

    ~s({"tests": #{tests}, "files": {#{entries}}})
  end

  defp quoted(text) do
    escaped =
      for <<char::utf8 <- text>>, into: "" do
        case char do
          ?" -> "\\\""
          ?\\ -> "\\\\"
          char when char < 0x20 -> "\\u" <> String.pad_leading(Integer.to_string(char, 16), 4, "0")
          char -> <<char::utf8>>
        end
      end

    ~s("#{escaped}")
  end
end
