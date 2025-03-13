import json
import subprocess
import sys

def run_benchmark(num_users, num_rounds, system_prompt, chat_history, answer_len, model, base_url, qps, output_file):
    """Run the benchmark using the provided parameters."""
    command = [
        'python3', './multi-round-qa.py',
        '--num-users', str(num_users),
        '--num-rounds', str(num_rounds),
        '--qps', str(qps),
        '--shared-system-prompt', str(system_prompt),
        '--user-history-prompt', str(chat_history),
        '--answer-len', str(answer_len),
        '--model', model,
        '--base-url', base_url,
        '--output', output_file,
        '--log-interval', '30',
        '--time', '100'
    ]
    # Warmup run 
    subprocess.run(command, check=True)
    # Real run
    subprocess.run(command, check=True)

def main(config_file, model, base_url, key):
    """Read configuration from a JSONL file and execute benchmarks."""
    try:
        with open(config_file, 'r') as file:
            for line in file:
                config = json.loads(line.strip())
                num_users = config.get('NUM_USERS', 320)
                num_rounds = config.get('NUM_ROUNDS', 10)
                system_prompt = config.get('SYSTEM_PROMPT', 1000)
                chat_history = config.get('CHAT_HISTORY', 20000)
                answer_len = config.get('ANSWER_LEN', 100)
                
                # Run benchmarks for different QPS values
                for qps in [0.1, 0.5, 0.9, 1.3, 1.7, 2.1]:
                    output_file = f"{key}_qps_{qps}_users_{num_users}_rounds_{num_rounds}_prompt_{system_prompt}_history_{chat_history}_answer_{answer_len}.csv"
                    run_benchmark(num_users, num_rounds, system_prompt, chat_history, answer_len, model, base_url, qps, output_file)
    except FileNotFoundError:
        print(f"Error: The configuration file '{config_file}' was not found.")
    except json.JSONDecodeError as e:
        print(f"Error decoding JSON: {e}")
    except subprocess.CalledProcessError as e:
        print(f"An error occurred while running the benchmark: {e}")

if __name__ == '__main__':
    if len(sys.argv) != 5:
        print("Usage: python3 script.py <config_file> <model> <base_url> <save_file_key>")
        sys.exit(1)
    
    config_file = sys.argv[1]
    model = sys.argv[2]
    base_url = sys.argv[3]
    key = sys.argv[4]
    
    main(config_file, model, base_url, key)
